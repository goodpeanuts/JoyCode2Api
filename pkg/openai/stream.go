package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

// connectStreamWithRetry attempts to connect to upstream with retries.
// Peeks at the first SSE line to detect errors before returning the response,
// so upstream failures surface as real HTTP error statuses instead of empty
// 200 streams (mirrors the anthropic handler's connectStreamWithRetry).
func (s *Server) connectStreamWithRetry(r *http.Request, jcBody map[string]interface{}, client *joycode.Client) (*http.Response, error) {
	maxRetries := 3
	if s.store != nil {
		maxRetries = s.store.GetIntSetting("max_retries", 3)
	}
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := client.PostStream(chatEndpoint, jcBody)
		if err != nil {
			lastErr = err
			slog.Error("stream connect error", "attempt", attempt, "max", maxRetries, "error", err)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}

		br := bufio.NewReaderSize(resp.Body, 64*1024)
		firstLine, err := br.ReadString('\n')
		if err != nil {
			resp.Body.Close()
			lastErr = fmt.Errorf("read first line: %w", err)
			slog.Error("stream read first line", "attempt", attempt, "max", maxRetries, "error", lastErr)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}

		trimmed := strings.TrimSpace(firstLine)
		dataContent := strings.TrimPrefix(trimmed, "data: ")
		if isUpstreamError(dataContent) {
			resp.Body.Close()
			lastErr = fmt.Errorf("upstream error: %s", truncateStr(dataContent, 500))
			logUpstreamError(attempt, maxRetries, dataContent)
			// Store parsed error detail in context for middleware
			if detail := store.ParseUpstreamErrorDetail(dataContent); detail != "" {
				store.SetErrorDetail(r, detail)
			}
			// Context-limit and SENSITIVE_CONTENT errors are deterministic — retrying is pointless
			if isContextLimitError(dataContent) || strings.Contains(dataContent, "SENSITIVE_CONTENT") {
				return nil, lastErr
			}
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}

		// Check first line for content_filter (content + finish_reason in same chunk)
		if filtered, chunkData := extractContentFilterInfo(dataContent); filtered {
			resp.Body.Close()
			lastErr = fmt.Errorf("%s", truncateStr(chunkData, 500))
			slog.Warn("content_filter detected in first chunk, not retrying", "chunk", truncateStr(chunkData, 300))
			return nil, lastErr
		}

		// Peek second line to detect content_filter with separate finish_reason
		replayLines := firstLine
		secondLine, sErr := br.ReadString('\n')
		// Keep partial trailing data even when the stream ends mid-line
		replayLines += secondLine
		if sErr == nil {
			trimmedSecond := strings.TrimSpace(secondLine)
			dataSecond := strings.TrimPrefix(trimmedSecond, "data: ")
			if filtered, chunkData := extractContentFilterInfo(dataSecond); filtered {
				resp.Body.Close()
				lastErr = fmt.Errorf("%s", truncateStr(chunkData, 500))
				slog.Warn("content_filter detected in second chunk, not retrying", "chunk", truncateStr(chunkData, 300))
				return nil, lastErr
			}
		}

		// Wrap body to replay buffered lines for the pipe
		originalBody := resp.Body
		resp.Body = &prependReader{
			first:  []byte(replayLines),
			source: br,
			body:   originalBody,
		}
		slog.Info("stream connected", "attempt", attempt)
		return resp, nil
	}
	return nil, fmt.Errorf("stream failed after %d attempts: %w", maxRetries, lastErr)
}

// prependReader replays buffered lines before reading from the underlying source.
type prependReader struct {
	first  []byte
	offset int
	source io.Reader
	body   io.ReadCloser
}

func (r *prependReader) Read(p []byte) (int, error) {
	if r.offset < len(r.first) {
		n := copy(p, r.first[r.offset:])
		r.offset += n
		return n, nil
	}
	return r.source.Read(p)
}

func (r *prependReader) Close() error {
	return r.body.Close()
}

// isUpstreamError reports whether an SSE data line is an upstream error payload
// (no choices but error-ish fields), e.g. {"error":{"code":"1032",...}}.
func isUpstreamError(line string) bool {
	if line == "" || line == "[DONE]" {
		return false
	}
	var parsed struct {
		Choices []interface{} `json:"choices"`
		Error   interface{}   `json:"error"`
		Code    interface{}   `json:"code"`
		Status  string        `json:"status"`
		Msg     string        `json:"msg"`
	}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return false
	}
	if len(parsed.Choices) > 0 {
		return false
	}
	return parsed.Error != nil || parsed.Code != nil || parsed.Status != "" || parsed.Msg != ""
}

// isUpstreamErrorResponse reports whether a 200 JSON body from upstream is
// actually an error payload. A successful chat completion always carries a
// non-null choices field; upstream error bodies like {"error":{"code":"1032"}}
// have no choices and would otherwise translate to {"choices":null}.
func isUpstreamErrorResponse(resp map[string]interface{}) bool {
	if len(resp) == 0 {
		return true
	}
	if choices, ok := resp["choices"]; ok && choices != nil {
		return false
	}
	return true
}

// isContextLimitError checks if the upstream error indicates context length exceeded.
func isContextLimitError(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "context length") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "tokens exceeded") ||
		strings.Contains(lower, "input length") ||
		strings.Contains(lower, "model_context_window_exceeded") ||
		strings.Contains(lower, "prompt length") ||
		strings.Contains(lower, "max_input_tokens")
}

// extractContentFilterInfo checks if a SSE data line contains content_filter finish_reason.
// Returns whether content_filter was detected and the raw data line for error reporting.
func extractContentFilterInfo(line string) (bool, string) {
	if line == "" || line == "[DONE]" {
		return false, ""
	}
	var parsed struct {
		Choices []struct {
			FinishReason *string         `json:"finish_reason"`
			Message      json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return false, ""
	}
	for _, c := range parsed.Choices {
		if c.FinishReason != nil && *c.FinishReason == "content_filter" {
			return true, line
		}
	}
	return false, ""
}

// logUpstreamError logs the full upstream error response for diagnosis.
func logUpstreamError(attempt, maxAttempt int, body string) {
	// Try to extract structured error
	var errResp map[string]interface{}
	if json.Unmarshal([]byte(body), &errResp) == nil {
		if errObj, ok := errResp["error"].(map[string]interface{}); ok {
			slog.Error("upstream error",
				"attempt", attempt,
				"max", maxAttempt,
				"error_code", errObj["code"],
				"error_message", errObj["message"],
				"error_status", errObj["status"],
			)
			return
		}
	}
	// Fallback: log truncated raw body
	truncated := body
	if len(truncated) > 500 {
		truncated = truncated[:500]
	}
	slog.Error("upstream error (raw)",
		"attempt", attempt,
		"max", maxAttempt,
		"body", truncated,
	)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
