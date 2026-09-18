package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

// responsesEndpoint 是海外 GPT 模型（extJson.adapterType=openai-response）的
// 专用上游通道：JoyCode color gateway 的 responses_completions 函数，请求/响应
// 均为原生 OpenAI Responses API 格式。经 chat_completions 通道调用这些模型会被
// 平台以 {"error":{"code":"1032","message":"HTTP调用异常"}} 拒绝。
const responsesEndpoint = "/api/saas/openai/v1/responses"

// handleResponses implements POST /v1/responses — a passthrough of the OpenAI
// Responses API to the JoyCode responses_completions gateway function. Bodies
// are forwarded as-is (plus the JoyCode account wrapper injected by
// joycode.Client.Post/PostStream); only the model name is resolved.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("decode responses request", "error", err)
		writeError(w, 400, fmt.Sprintf("请求体解析失败: %s。请检查请求是否完整，或尝试开启新对话减少上下文长度。", err.Error()))
		return
	}
	reqModel, _ := req["model"].(string)
	systemDefault := ""
	if s.store != nil {
		systemDefault = s.store.GetSetting("default_model")
	}
	model, exact := ResolveModelMatch(reqModel, store.GetAccountDefaultModel(r), systemDefault, s.knownModels())
	display := joycode.DisplayModel(reqModel, model, exact)
	store.SetModel(r, display)
	stream, _ := req["stream"].(bool)
	slog.Info("responses request", "model", reqModel, "resolved", display, "stream", stream)
	req["model"] = model
	client := s.getClient(r)
	if stream {
		s.handleStreamResponses(w, r, client, req, model)
	} else {
		s.handleNonStreamResponses(w, r, client, req, model)
	}
}

func (s *Server) handleNonStreamResponses(w http.ResponseWriter, r *http.Request, client *joycode.Client, jcBody map[string]interface{}, model string) {
	maxRetries := 3
	if s.store != nil {
		maxRetries = s.store.GetIntSetting("max_retries", 3)
	}
	var resp map[string]interface{}
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, lastErr = client.Post(responsesEndpoint, jcBody)
		if lastErr != nil {
			slog.Error("responses non-stream upstream error", "model", model, "attempt", attempt, "max", maxRetries, "error", lastErr)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}
		// Upstream can return HTTP 200 with an error payload (e.g. code 1032).
		if isResponsesErrorResponse(resp) {
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
		slog.Error("responses non-stream failed after retries", "model", model, "error", lastErr)
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
		inTk, _ := usage["input_tokens"].(float64)
		outTk, _ := usage["output_tokens"].(float64)
		store.SetTokenUsage(r, int(inTk), int(outTk))
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleStreamResponses(w http.ResponseWriter, r *http.Request, client *joycode.Client, jcBody map[string]interface{}, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("streaming not supported by response writer")
		writeError(w, 500, "streaming not supported by response writer")
		return
	}

	// Commit SSE headers early, then start a heartbeat goroutine (mirrors the
	// chat handler: upstream TTFB can be 10–30s for reasoning models).
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(200)

	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		defer close(heartbeatDone)
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}()

	streamStart := time.Now()
	resp, err := s.connectResponsesStreamWithRetry(r, jcBody, client)
	close(stopHeartbeat)
	<-heartbeatDone
	if err != nil {
		slog.Error("responses stream upstream error", "model", model, "error", err)
		msg := err.Error()
		if isTimeoutError(msg) {
			msg = "上游服务响应超时，请稍后重试。原始错误: " + msg
		}
		writeResponsesStreamError(w, flusher, msg)
		return
	}
	defer resp.Body.Close()
	slog.Info("responses stream: connected to upstream", "model", model, "ttfb_ms", time.Since(streamStart).Milliseconds())

	// The gateway wraps each SSE line in an extra "data: " layer
	// ("data: event: X" / "data: data: {...}"); unwrap it and re-emit clean
	// Responses API events. The Responses API has no [DONE] terminator — the
	// response.completed/failed/incomplete event closes the stream.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var inTk, outTk int
	sawTerminal := false
	pendingEvent := ""
	for scanner.Scan() {
		payload := unwrapResponsesSSE(scanner.Text())
		if payload == "" {
			continue
		}
		if strings.HasPrefix(payload, "event: ") {
			pendingEvent = strings.TrimSpace(strings.TrimPrefix(payload, "event: "))
			continue
		}
		if payload == "[DONE]" || !strings.HasPrefix(payload, "{") {
			continue
		}
		eventName := pendingEvent
		if eventName == "" {
			eventName = responsesEventType(payload)
		}
		pendingEvent = ""
		if isResponsesTerminalEvent(eventName) {
			sawTerminal = true
		}
		if eventName != "" {
			fmt.Fprintf(w, "event: %s\n", eventName)
		}
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
		updateResponsesUsage(payload, &inTk, &outTk)
	}
	// Surface a premature upstream close (no terminal event) as a stream
	// failure instead of letting the client hang or treat it as done.
	if !sawTerminal {
		if err := scanner.Err(); err != nil {
			slog.Error("responses stream ended abnormally before terminal event", "model", model, "error", err)
			writeResponsesStreamError(w, flusher, "读取上游流式响应失败，本次回复不完整，请重试。原始错误: "+err.Error())
		} else {
			slog.Warn("responses stream closed before terminal event (premature upstream close)", "model", model)
			writeResponsesStreamError(w, flusher, "上游在返回结束标记前断开了流式响应，本次回复可能不完整，请重试。")
		}
	} else if err := scanner.Err(); err != nil {
		slog.Error("responses stream scanner error", "model", model, "error", err)
	}
	if inTk > 0 || outTk > 0 {
		store.SetTokenUsage(r, inTk, outTk)
	}
}

// connectResponsesStreamWithRetry connects to the responses_completions
// upstream with retries, peeking at the first SSE line (after unwrapping the
// gateway's extra data: layer) to detect error payloads before piping.
func (s *Server) connectResponsesStreamWithRetry(r *http.Request, jcBody map[string]interface{}, client *joycode.Client) (*http.Response, error) {
	maxRetries := 3
	if s.store != nil {
		maxRetries = s.store.GetIntSetting("max_retries", 3)
	}
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := client.PostStream(responsesEndpoint, jcBody)
		if err != nil {
			lastErr = err
			slog.Error("responses stream connect error", "attempt", attempt, "max", maxRetries, "error", lastErr)
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
			slog.Error("responses stream read first line", "attempt", attempt, "max", maxRetries, "error", lastErr)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}

		dataContent := unwrapResponsesSSE(firstLine)
		if isResponsesErrorPayload(dataContent) {
			resp.Body.Close()
			lastErr = fmt.Errorf("upstream error: %s", truncateStr(dataContent, 500))
			logUpstreamError(attempt, maxRetries, dataContent)
			if detail := store.ParseUpstreamErrorDetail(dataContent); detail != "" {
				store.SetErrorDetail(r, detail)
			}
			if isContextLimitError(dataContent) || strings.Contains(dataContent, "SENSITIVE_CONTENT") {
				return nil, lastErr
			}
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
			continue
		}

		resp.Body = &prependReader{first: []byte(firstLine), source: br, body: resp.Body}
		slog.Info("responses stream connected", "attempt", attempt)
		return resp, nil
	}
	return nil, fmt.Errorf("stream failed after %d attempts: %w", maxRetries, lastErr)
}

// writeResponsesStreamError emits a response.failed event in-band. Used after
// SSE headers are committed; the Responses API signals stream failures via
// response.failed (there is no [DONE] terminator).
func writeResponsesStreamError(w http.ResponseWriter, flusher http.Flusher, msg string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "response.failed",
		"response": map[string]interface{}{
			"id":    "",
			"error": map[string]string{"code": "api_error", "message": msg},
		},
	})
	fmt.Fprintf(w, "event: response.failed\n")
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

// unwrapResponsesSSE strips the gateway's nested "data:" prefixes from a raw
// SSE line: "data: data: {...}" → "{...}", "data: event: X" → "event: X".
func unwrapResponsesSSE(line string) string {
	trimmed := strings.TrimSpace(line)
	for {
		next := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if next == trimmed {
			return trimmed
		}
		trimmed = next
	}
}

// isResponsesErrorPayload reports whether an unwrapped SSE payload is an
// upstream error body such as {"error":{"code":"1032",...}}. Valid Responses
// API events always carry a "type" (events) or "object" (response envelope)
// field, and response envelopes may carry "status":"completed" — so the chat
// path's isUpstreamError (which treats any status as an error) cannot be used.
func isResponsesErrorPayload(payload string) bool {
	if payload == "" || payload == "[DONE]" {
		return false
	}
	var parsed struct {
		Type   string      `json:"type"`
		Object string      `json:"object"`
		Error  interface{} `json:"error"`
		Code   interface{} `json:"code"`
		Msg    string      `json:"msg"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return false
	}
	if parsed.Type != "" || parsed.Object != "" {
		return false
	}
	return parsed.Error != nil || parsed.Code != nil || parsed.Msg != ""
}

// isResponsesErrorResponse is the non-stream counterpart of
// isResponsesErrorPayload for a decoded JSON body.
func isResponsesErrorResponse(resp map[string]interface{}) bool {
	if len(resp) == 0 {
		return true
	}
	if t, _ := resp["type"].(string); strings.HasPrefix(t, "response") {
		return false
	}
	if obj, _ := resp["object"].(string); obj != "" {
		return false
	}
	if resp["error"] != nil {
		return true
	}
	if resp["code"] != nil {
		return true
	}
	if msg, _ := resp["msg"].(string); msg != "" {
		return true
	}
	return false
}

func responsesEventType(payload string) string {
	var e struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(payload), &e) == nil {
		return e.Type
	}
	return ""
}

func isResponsesTerminalEvent(name string) bool {
	switch name {
	case "response.completed", "response.failed", "response.incomplete":
		return true
	}
	return false
}

// updateResponsesUsage extracts token usage from terminal event payloads
// ({"type":"response.completed","response":{...,"usage":{...}}}).
func updateResponsesUsage(payload string, inTk, outTk *int) {
	var e struct {
		Response struct {
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(payload), &e) != nil || e.Response.Usage == nil {
		return
	}
	if e.Response.Usage.InputTokens > 0 || e.Response.Usage.OutputTokens > 0 {
		*inTk = e.Response.Usage.InputTokens
		*outTk = e.Response.Usage.OutputTokens
	}
}
