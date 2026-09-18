package openai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

const chatEndpoint = "/api/saas/openai/v1/chat/completions"

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("decode chat request", "error", err)
		writeError(w, 400, fmt.Sprintf("请求体解析失败: %s。请检查请求是否完整，或尝试开启新对话减少上下文长度。", err.Error()))
		return
	}
	systemDefault := ""
	if s.store != nil {
		systemDefault = s.store.GetSetting("default_model")
	}
	model, exact := ResolveModelMatch(req.Model, store.GetAccountDefaultModel(r), systemDefault, s.knownModels())
	display := joycode.DisplayModel(req.Model, model, exact)
	store.SetModel(r, display)
	slog.Info("openai request", "model", req.Model, "resolved", display, "stream", req.Stream)
	req.Model = model
	jcBody := TranslateRequest(&req)
	client := s.getClient(r)
	if req.Stream {
		s.handleStreamChat(w, r, client, jcBody, model)
	} else {
		s.handleNonStreamChat(w, r, client, jcBody, model)
	}
}

func (s *Server) handleNonStreamChat(w http.ResponseWriter, r *http.Request, client *joycode.Client, jcBody map[string]interface{}, model string) {
	maxRetries := 3
	if s.store != nil {
		maxRetries = s.store.GetIntSetting("max_retries", 3)
	}
	var resp map[string]interface{}
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, lastErr = client.Post(chatEndpoint, jcBody)
		if lastErr != nil {
			slog.Error("chat non-stream upstream error", "model", model, "attempt", attempt, "max", maxRetries, "error", lastErr)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}
		// Upstream can return HTTP 200 with an error payload (e.g. code 1032);
		// retry instead of passing {"choices":null} through as a success.
		if isUpstreamErrorResponse(resp) {
			raw, _ := json.Marshal(resp)
			lastErr = fmt.Errorf("upstream error: %s", truncateStr(string(raw), 500))
			logUpstreamError(attempt, maxRetries, string(raw))
			if detail := store.ParseUpstreamErrorDetail(string(raw)); detail != "" {
				store.SetErrorDetail(r, detail)
			}
			// Deterministic errors — retrying is pointless
			if isContextLimitError(string(raw)) || strings.Contains(string(raw), "SENSITIVE_CONTENT") {
				break
			}
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}
		break
	}

	if lastErr != nil {
		slog.Error("chat non-stream failed after retries", "model", model, "error", lastErr)
		msg := lastErr.Error()
		if detail := store.ParseUpstreamErrorDetail(msg); detail != "" {
			store.SetErrorDetail(r, detail)
		}
		code := 500
		if isTimeoutError(msg) {
			code = 504
			msg = "上游服务响应超时，请稍后重试。原始错误: " + msg
		}
		writeError(w, code, msg)
		return
	}
	if usage, ok := resp["usage"].(map[string]interface{}); ok {
		inTk, _ := usage["prompt_tokens"].(float64)
		outTk, _ := usage["completion_tokens"].(float64)
		store.SetTokenUsage(r, int(inTk), int(outTk))
	}
	writeJSON(w, 200, TranslateResponse(resp, model))
}

func (s *Server) handleStreamChat(w http.ResponseWriter, r *http.Request, client *joycode.Client, jcBody map[string]interface{}, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("streaming not supported by response writer")
		writeError(w, 500, "streaming not supported by response writer")
		return
	}

	// Connect upstream and probe the first SSE line before committing response
	// headers, so upstream failures surface as real HTTP error statuses
	// instead of an empty 200 stream.
	resp, err := s.connectStreamWithRetry(r, jcBody, client)
	if err != nil {
		slog.Error("chat stream upstream error", "model", model, "error", err)
		msg := err.Error()
		code := 500
		if isTimeoutError(msg) {
			code = 504
			msg = "上游服务响应超时，请稍后重试。原始错误: " + msg
		}
		writeError(w, code, msg)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(200)

	// Pipe JoyCode SSE response directly — already OpenAI-compatible format
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if readErr != nil {
			if readErr.Error() != "EOF" {
				slog.Error("chat stream read error", "model", model, "error", readErr)
				// Headers are already committed; emit an in-band error event so
				// the client sees the failure instead of a silent truncation.
				writeStreamErrorEvent(w, flusher, readErr.Error())
			}
			break
		}
	}
}

// writeStreamErrorEvent emits an OpenAI-style in-band SSE error followed by
// [DONE]. Used after response headers are committed and the status code can
// no longer be changed.
func writeStreamErrorEvent(w http.ResponseWriter, flusher http.Flusher, msg string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{"message": msg, "type": "api_error"},
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func isTimeoutError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "client.timeout exceeded") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "i/o timeout")
}
