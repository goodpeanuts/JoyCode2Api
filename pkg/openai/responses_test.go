package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupResponsesServerWithBackend wires a /v1/responses frontend to a mock
// JoyCode upstream (mirrors setupChatServerWithBackend, with retries=1).
func setupResponsesServerWithBackend(t *testing.T, upstream http.Handler) *httptest.Server {
	t.Helper()
	backend := httptest.NewServer(upstream)
	client := newMockClient(backend)
	st, storeCleanup, err := newTempStore()
	if err != nil {
		backend.Close()
		t.Fatalf("newTempStore: %v", err)
	}
	if err := st.SetSetting("max_retries", "1"); err != nil {
		t.Fatalf("set max_retries: %v", err)
	}
	srv := NewServer(client, st)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	frontend := httptest.NewServer(mux)
	t.Cleanup(func() {
		frontend.Close()
		backend.Close()
		storeCleanup()
	})
	return frontend
}

func TestResponses_Options(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called")
	}))
	req, _ := http.NewRequest("OPTIONS", frontend.URL+"/v1/responses", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestResponses_Get(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called")
	}))
	resp, err := http.Get(frontend.URL + "/v1/responses")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestResponses_InvalidJSON(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called")
	}))
	resp, err := http.Post(frontend.URL+"/v1/responses", "application/json", strings.NewReader("{invalid"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestResponses_NonStreamPassthrough(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/saas/openai/v1/responses" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, `{"object":"response","id":"resp_1","status":"completed","model":"gpt-6-astra","output":[],"usage":{"input_tokens":11,"output_tokens":5}}`)
	}))

	body := `{"model":"GPT-6 Astra","stream":false,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	resp, err := http.Post(frontend.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"object":"response"`) || !strings.Contains(string(raw), `"usage"`) {
		t.Errorf("response body should pass through the Responses envelope, got: %s", raw)
	}
}

func TestResponses_NonStreamUpstreamErrorReturns500(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, `{"error":{"code":"1032","message":"HTTP调用异常"}}`)
	}))

	body := `{"model":"GPT-6 Astra","stream":false,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	resp, err := http.Post(frontend.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "1032") {
		t.Errorf("error body should carry upstream code 1032, got: %s", raw)
	}
}

// gatewaySSE writes the JoyCode gateway's nested SSE format:
// "data: event: X" lines followed by "data: data: {...}" lines.
func gatewaySSE(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "data: event: %s\n\ndata: data: %s\n\n", event, data)
}

func TestResponses_StreamNormalSSEUnwrap(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		gatewaySSE(w, "response.created", `{"type":"response.created","response":{"id":"resp_1"}}`)
		gatewaySSE(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"hello"}`)
		gatewaySSE(w, "response.completed", `{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":11,"output_tokens":5}}}`)
	}))

	body := `{"model":"GPT-6 Astra","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	resp, err := http.Post(frontend.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, raw)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	for _, want := range []string{
		"event: response.created\n",
		"event: response.output_text.delta\n",
		"event: response.completed\n",
		`"delta":"hello"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream output should contain %q, got: %s", want, out)
		}
	}
	if strings.Contains(out, "data: data:") || strings.Contains(out, "data: event:") {
		t.Errorf("stream output should have the gateway data layer unwrapped, got: %s", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Errorf("Responses API streams must not carry a [DONE] terminator, got: %s", out)
	}
}

func TestResponses_StreamUpstreamErrorInBand(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"error\":{\"code\":\"1032\",\"message\":\"HTTP调用异常\"}}\n\n")
	}))

	body := `{"model":"GPT-6 Astra","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	resp, err := http.Post(frontend.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 (in-band SSE error), got %d: %s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	if !strings.Contains(out, "event: response.failed") {
		t.Errorf("stream error should surface as a response.failed event, got: %s", out)
	}
	if !strings.Contains(out, "1032") {
		t.Errorf("stream error should carry upstream code 1032, got: %s", out)
	}
}

func TestResponses_StreamPrematureCloseEmitsFailure(t *testing.T) {
	frontend := setupResponsesServerWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// No terminal event — upstream closes mid-response.
		gatewaySSE(w, "response.created", `{"type":"response.created","response":{"id":"resp_1"}}`)
	}))

	body := `{"model":"GPT-6 Astra","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	resp, err := http.Post(frontend.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	if !strings.Contains(out, "event: response.failed") {
		t.Errorf("premature close should emit response.failed, got: %s", out)
	}
}

func TestResponses_ErrorPayloadClassification(t *testing.T) {
	cases := []struct {
		payload string
		want    bool
	}{
		{`{"error":{"code":"1032","message":"HTTP调用异常"}}`, true},
		{`{"code":6002,"msg":"模型不存在"}`, true},
		{`{"type":"response.created","response":{"id":"resp_1"}}`, false},
		{`{"type":"response.completed","response":{"status":"completed","usage":{}}}`, false},
		{`{"object":"response","id":"resp_1","status":"completed"}`, false},
		{``, false},
		{`[DONE]`, false},
		{`not json`, false},
	}
	for _, c := range cases {
		if got := isResponsesErrorPayload(c.payload); got != c.want {
			t.Errorf("isResponsesErrorPayload(%s) = %v, want %v", c.payload, got, c.want)
		}
	}
}

func TestResponses_UnwrapSSE(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"data: data: {\"a\":1}", "{\"a\":1}"},
		{"data: event: response.created", "event: response.created"},
		{"data: {\"a\":1}", "{\"a\":1}"},
		{"", ""},
		{": keepalive", ": keepalive"},
	}
	for _, c := range cases {
		if got := unwrapResponsesSSE(c.in); got != c.want {
			t.Errorf("unwrapResponsesSSE(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
