# 上游错误详情记录与展示 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将上游服务端返回的结构化错误信息（error_code, error_type, error_message, error_status）解析后存入数据库，并在前端用户详情页的请求日志展开行中展示。

**Architecture:** 在 handler 层解析上游错误 JSON 并写入 request context → 中间件从 context 读取并传入 store.LogRequest → 数据库新增 error_detail JSON 列 → API 返回该字段 → 前端解析并渲染为 Descriptions 组件。

**Tech Stack:** Go (slog, encoding/json, net/http), SQLite, React + TypeScript + Ant Design

## Global Constraints

- 不修改返回给客户端的 HTTP 响应体格式
- 遵循现有 context 传递模式（参考 `pkg/store/token_usage.go`）
- 遵循现有数据库 migration 模式（ALTER TABLE ADD COLUMN，忽略重复列错误）
- 前端保持现有 Ant Design 组件风格

---

### Task 1: 新增 error_detail context 传递机制

**Files:**
- Create: `pkg/store/error_detail.go`

**Interfaces:**
- Produces: `SetErrorDetail(r *http.Request, detail string)` — 写入 request context
- Produces: `GetErrorDetail(r *http.Request) string` — 从 request context 读取

- [x] **Step 1: 创建 `pkg/store/error_detail.go`**

```go
package store

import (
	"context"
	"net/http"
)

type errorDetailCtxKey struct{}

// SetErrorDetail stores parsed upstream error detail in request context.
func SetErrorDetail(r *http.Request, detail string) {
	*r = *r.WithContext(context.WithValue(r.Context(), errorDetailCtxKey{}, detail))
}

// GetErrorDetail retrieves upstream error detail from request context.
func GetErrorDetail(r *http.Request) string {
	if v, ok := r.Context().Value(errorDetailCtxKey{}).(string); ok {
		return v
	}
	return ""
}
```

- [x] **Step 2: 验证编译通过**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api && go build ./pkg/store/...
```

- [x] **Step 3: Commit**

```bash
git add pkg/store/error_detail.go
git commit -m "feat: add error_detail context pass-through for upstream error parsing"
```

---

### Task 2: 新增 parseUpstreamErrorDetail 工具函数

**Files:**
- Modify: `pkg/anthropic/logger.go` — 在文件末尾追加

**Interfaces:**
- Produces: `parseUpstreamErrorDetail(raw string) string` — 输入 client 返回的 error 字符串或 SSE data 行，输出 JSON `{"error_code":"...","error_message":"...","error_type":"...","error_status":...}`

- [ ] **Step 1: 在 `pkg/anthropic/logger.go` 末尾追加函数**

```go
// parseUpstreamErrorDetail extracts structured error info from an upstream error
// string (either a client.Post error like "API error 400: {...}" or an SSE data
// line like "{\"error\":{...}}"). Returns a JSON string with error_code,
// error_message, error_type, error_status fields, or "" on parse failure.
func parseUpstreamErrorDetail(raw string) string {
	idx := strings.Index(raw, "{")
	if idx < 0 {
		return ""
	}
	body := raw[idx:]
	var errResp map[string]interface{}
	if json.Unmarshal([]byte(body), &errResp) != nil {
		return ""
	}
	if errObj, ok := errResp["error"].(map[string]interface{}); ok {
		detail := map[string]interface{}{
			"error_code":    errObj["code"],
			"error_message": errObj["message"],
			"error_type":    errObj["type"],
			"error_status":  errObj["status"],
		}
		b, _ := json.Marshal(detail)
		return string(b)
	}
	return ""
}
```

需要补充 import: `"strings"` 和 `"encoding/json"`（`logger.go` 已有这两个 import）。

- [x] **Step 2: 验证编译通过**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api && go build ./pkg/anthropic/...
```

- [x] **Step 3: Commit**

```bash
git add pkg/anthropic/logger.go
git commit -m "feat: add parseUpstreamErrorDetail to extract structured upstream error info"
```

---

### Task 3: 数据库 migration — 新增 error_detail 列

**Files:**
- Modify: `pkg/store/store.go` — `migrate()` 函数中追加 migration
- Modify: `pkg/store/store.go` — `RequestLog` struct 增加 `ErrorDetail` 字段
- Modify: `pkg/store/store.go` — `LogRequest` 函数签名增加 `errorDetail` 参数
- Modify: `pkg/store/store.go` — `GetAccountLogsQuery` 查询增加 `error_detail` 列
- Modify: `pkg/store/store.go` — `GetRecentErrors` 查询增加 `error_detail` 列

**Interfaces:**
- Consumes: `GetErrorDetail(r *http.Request) string` (from Task 1)
- Produces: `RequestLog.ErrorDetail string` (json tag `error_detail`)

- [ ] **Step 1: 在 `migrate()` 中追加 migration（在 `idx_request_logs_api_key_id` 索引创建之前）**

在 `store.go` 第 350 行之前插入：

```go
	// Migration: add error_detail column to request_logs
	s.db.Exec("ALTER TABLE request_logs ADD COLUMN error_detail TEXT DEFAULT ''")
```

- [ ] **Step 2: 在 `RequestLog` struct 中增加字段**

```go
type RequestLog struct {
	ID           int64  `json:"id"`
	UserID       string `json:"user_id"`
	Model        string `json:"model"`
	Endpoint     string `json:"endpoint"`
	Stream       bool   `json:"stream"`
	StatusCode   int    `json:"status_code"`
	LatencyMs    int64  `json:"latency_ms"`
	ErrorMessage string `json:"error_message"`
	ErrorDetail string `json:"error_detail"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CreatedAt    string `json:"created_at"`
}
```

- [ ] **Step 3: 修改 `LogRequest` 函数签名和 INSERT**

```go
func (s *Store) LogRequest(userID, model, endpoint string, stream bool, statusCode int, latencyMs int64, errMsg string, errorDetail string, inputTokens, outputTokens int) error {
	sInt := 0
	if stream {
		sInt = 1
	}
	_, err := s.db.Exec(
		"INSERT INTO request_logs (api_key, model, endpoint, stream, status_code, latency_ms, error_message, error_detail, input_tokens, output_tokens) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		userID, model, endpoint, sInt, statusCode, latencyMs, errMsg, errorDetail, inputTokens, outputTokens,
	)
	if err != nil {
		slog.Error("store: log request failed", "user_id", userID, "endpoint", endpoint, "error", err)
	}
	return err
}
```

- [ ] **Step 4: 修改 `GetAccountLogsQuery` 的 SELECT 和 Scan**

```go
rows, err := s.db.Query(
	"SELECT id, api_key, model, endpoint, stream, status_code, latency_ms, COALESCE(error_message, ''), COALESCE(error_detail, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), created_at FROM request_logs WHERE "+where+" ORDER BY id DESC LIMIT ?",
	args...,
)
```

Scan 行：

```go
if err := rows.Scan(&l.ID, &l.UserID, &l.Model, &l.Endpoint, &streamInt, &l.StatusCode, &l.LatencyMs, &l.ErrorMessage, &l.ErrorDetail, &l.InputTokens, &l.OutputTokens, &l.CreatedAt); err != nil {
	return nil, err
}
```

- [ ] **Step 5: 修改 `GetRecentErrors` 的 SELECT 和 Scan**

```go
rows, err := s.db.Query(
	"SELECT id, api_key, model, endpoint, stream, status_code, latency_ms, COALESCE(error_message, ''), COALESCE(error_detail, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), created_at FROM request_logs WHERE status_code >= 400 ORDER BY id DESC LIMIT ?",
	limit,
)
```

Scan 行：

```go
if err := rows.Scan(&l.ID, &l.UserID, &l.Model, &l.Endpoint, &streamInt, &l.StatusCode, &l.LatencyMs, &l.ErrorMessage, &l.ErrorDetail, &l.InputTokens, &l.OutputTokens, &l.CreatedAt); err != nil {
	slog.Error("store: get recent errors scan failed", "error", err)
	return nil, err
}
```

- [ ] **Step 6: 验证编译通过**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api && go build ./...
```

- [ ] **Step 7: Commit**

```bash
git add pkg/store/store.go
git commit -m "feat: add error_detail column to request_logs and wire through queries"
```

---

### Task 4: 在 handler 层解析上游错误并写入 context

**Files:**
- Modify: `pkg/anthropic/handler.go` — `handleNonStream` 方法（约 163-180 行）
- Modify: `pkg/anthropic/handler.go` — `handleStream` 方法（约 264-280 行）
- Modify: `pkg/anthropic/handler.go` — `connectStreamWithRetry` 方法（约 854-868 行）

**Interfaces:**
- Consumes: `parseUpstreamErrorDetail(raw string) string` (from Task 2)
- Consumes: `store.SetErrorDetail(r *http.Request, detail string)` (from Task 1)

- [ ] **Step 1: 在 `handleNonStream` 的 `lastErr != nil` 分支中（第 163 行之后）写入 context**

在第 163 行 `if lastErr != nil {` 块内，紧接着 `errMsg := lastErr.Error()` 之后插入：

```go
if lastErr != nil {
	errMsg := lastErr.Error()
	// Parse upstream error detail and store in context for middleware logging
	if detail := parseUpstreamErrorDetail(errMsg); detail != "" {
		store.SetErrorDetail(r, detail)
	}
	if isContextLimitError(errMsg) {
		// ... existing code unchanged ...
```

- [ ] **Step 2: 在 `handleStream` 的最终错误分支中（第 264 行之后）写入 context**

在第 264 行 `if err != nil {` 块内，紧接着 `errMsg := err.Error()` 之后插入：

```go
if err != nil {
	errMsg := err.Error()
	// Parse upstream error detail and store in context for middleware logging
	if detail := parseUpstreamErrorDetail(errMsg); detail != "" {
		store.SetErrorDetail(r, detail)
	}
	if isContextLimitError(errMsg) {
		// ... existing code unchanged ...
```

- [ ] **Step 3: 在 `connectStreamWithRetry` 的 upstream error 分支中（第 854-868 行）写入 context**

在第 857 行 `logUpstreamError(r, attempt, maxRetries, dataContent)` 之后插入：

```go
if isUpstreamError(dataContent) {
	resp.Body.Close()
	lastErr = fmt.Errorf("upstream error: %s", truncate(dataContent, 500))
	logUpstreamError(r, attempt, maxRetries, dataContent)
	// Store parsed error detail in context for middleware
	if detail := parseUpstreamErrorDetail(dataContent); detail != "" {
		store.SetErrorDetail(r, detail)
	}
	if isContextLimitError(dataContent) {
		// ... existing code unchanged ...
```

- [ ] **Step 4: 验证编译通过**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add pkg/anthropic/handler.go
git commit -m "feat: parse upstream error detail in handler and write to request context"
```

---

### Task 5: 中间件收集 error_detail 并传入 LogRequest

**Files:**
- Modify: `cmd/JoyCodeProxy/serve.go` — `requestLogMiddleware` 函数（约 434-460 行）

**Interfaces:**
- Consumes: `store.GetErrorDetail(r *http.Request) string` (from Task 1)
- Consumes: `s.LogRequest(..., errorDetail string, ...)` (from Task 3, updated signature)

- [ ] **Step 1: 在 `requestLogMiddleware` 中读取 errorDetail 并传入 LogRequest**

在第 434 行 `var errMsg string` 之后，新增 `var errorDetail string`。在第 459-460 行的 `LogRequest` 调用中增加 `errorDetail` 参数：

```go
var errMsg string
var errorDetail string
if rw.statusCode >= 400 {
	reqID := atomic.AddUint64(&requestCounter, 1)
	errMsg = fmt.Sprintf("HTTP %d on %s %s", rw.statusCode, r.Method, path)
	if body := strings.TrimSpace(rw.body.String()); body != "" {
		errMsg = fmt.Sprintf("%s\n%s", errMsg, body)
	}
	// Collect upstream error detail from context (set by proxy handler)
	errorDetail = store.GetErrorDetail(r)
	slog.Error("proxy error response",
		"request_id", reqID,
		"status", rw.statusCode,
		"method", r.Method,
		"path", path,
		"model", model,
		"latency_ms", latency,
		"api_key", apiKey,
		"error", errMsg,
	)
}

var inTk, outTk int
inTk, outTk = store.GetTokenUsage(r)
resolvedModel := store.GetModel(r)
if resolvedModel != "" {
	model = resolvedModel
}
if s.GetSetting("enable_request_logging") != "false" {
	go s.LogRequest(apiKey, model, path, isStream, rw.statusCode, latency, errMsg, errorDetail, inTk, outTk)
}
```

- [x] **Step 2: 验证编译通过**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api && go build ./...
```

- [x] **Step 3: Commit**

```bash
git add cmd/JoyCodeProxy/serve.go
git commit -m "feat: collect error_detail from context and pass to LogRequest"
```

---

### Task 6: 前端 — API 类型和页面展示

**Files:**
- Modify: `web/src/api.ts` — `RequestLog` 接口增加 `error_detail` 字段
- Modify: `web/src/pages/AccountDetail.tsx` — 展开行错误详情区域（约 838-861 行）

**Interfaces:**
- Consumes: `RequestLog.error_detail` (JSON string from API)

- [ ] **Step 1: 在 `api.ts` 的 `RequestLog` 接口中增加字段**

```typescript
export interface RequestLog {
  id: number;
  user_id: string;
  model: string;
  endpoint: string;
  stream: boolean;
  status_code: number;
  latency_ms: number;
  error_message: string;
  error_detail: string;
  input_tokens: number;
  output_tokens: number;
  created_at: string;
}
```

- [ ] **Step 2: 在 `AccountDetail.tsx` 的展开行错误详情区域替换展示逻辑**

将第 838-861 行的错误详情展示代码替换为：

```tsx
{record.status_code >= 400 && (
  <div style={{
    marginBottom: 10,
    padding: '10px 12px',
    border: '1px solid #ffccc7',
    borderRadius: 6,
    background: '#fff2f0',
  }}>
    <Typography.Text strong style={{ display: 'block', marginBottom: 6, color: '#cf1322' }}>
      错误详情
    </Typography.Text>
    {(() => {
      if (record.error_detail) {
        try {
          const detail = JSON.parse(record.error_detail);
          return (
            <Descriptions size="small" column={2} colon={false}>
              <Descriptions.Item label="错误码">
                <Typography.Text code style={{ fontSize: 12 }}>{detail.error_code || '-'}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="错误类型">
                <Typography.Text code style={{ fontSize: 12 }}>{detail.error_type || '-'}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="状态码">
                <Typography.Text>{detail.error_status || '-'}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="错误信息" span={2}>
                <Typography.Text style={{ fontSize: 12 }}>{detail.error_message || '-'}</Typography.Text>
              </Descriptions.Item>
            </Descriptions>
          );
        } catch {
          // fall through to raw error_message
        }
      }
      return (
        <pre style={{
          margin: 0,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          fontSize: 12,
          lineHeight: 1.6,
          color: '#cf1322',
          fontFamily: 'monospace',
        }}>
          {record.error_message || `HTTP ${record.status_code}`}
        </pre>
      );
    })()}
  </div>
)}
```

需要确认 `Descriptions` 组件已 import（检查文件顶部 import 语句）。

- [ ] **Step 3: 检查 `Descriptions` 是否已 import**

```bash
grep -n "Descriptions" /Users/zenghao.87/repo/JoyCode2Api/web/src/pages/AccountDetail.tsx | head -5
```

如果未 import，在 antd import 行中加入 `Descriptions`。

- [ ] **Step 4: 构建前端验证编译通过**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api/web && npm run build
```

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/pages/AccountDetail.tsx
git commit -m "feat: display structured upstream error detail in request log expandable rows"
```

---

### Task 7: 端到端验证

- [ ] **Step 1: 启动服务并触发一个已知会失败的请求**

```bash
cd /Users/zenghao.87/repo/JoyCode2Api && go run ./cmd/JoyCodeProxy/...
```

- [ ] **Step 2: 检查数据库中的 error_detail 列**

```bash
sqlite3 /path/to/joycode.db "SELECT id, status_code, error_message, error_detail FROM request_logs WHERE status_code >= 400 ORDER BY id DESC LIMIT 3"
```

预期：`error_detail` 列包含 JSON 如 `{"error_code":"SENSITIVE_CONTENT","error_message":"...","error_type":"invalid_request_error","error_status":400}`。

- [ ] **Step 3: 检查 API 响应**

```bash
curl -s "http://localhost:8080/api/accounts/{userId}/logs?limit=5&filter=errors" | jq '.logs[0].error_detail'
```

预期：返回 JSON 字符串。

- [ ] **Step 4: 打开前端页面验证展开行展示**

访问用户详情页 → 请求日志 → 点击错误行展开 → 确认看到结构化错误字段（错误码、错误类型、状态码、错误信息）。

- [x] **Step 5: Commit**

```bash
git add -A
git commit -m "verify: end-to-end validation of structured error detail flow"
```

---

## 实施记录

### 实际提交历史

```
e5c89a2 chore: update test files and frontend build output for error_detail feature
5f85743 test: update LogRequest calls with errorDetail parameter
2f47df5 feat: display structured upstream error detail in request log expandable rows
e462118 feat: collect error_detail from context and pass to LogRequest
849a732 feat: parse upstream error detail in handler and write to request context
c89f08b feat: add error_detail column to request_logs and wire through queries
16d8b77 feat: add parseUpstreamErrorDetail to extract structured upstream error info
3c39ac3 feat: add error_detail context pass-through for upstream error parsing
```

### 改动文件清单

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `pkg/store/error_detail.go` | 新建 | context 传递机制：SetErrorDetail / GetErrorDetail |
| `pkg/anthropic/logger.go` | 修改 | 新增 parseUpstreamErrorDetail 工具函数 |
| `pkg/store/store.go` | 修改 | migration + RequestLog struct + LogRequest 签名 + 查询 Scan |
| `pkg/anthropic/handler.go` | 修改 | handleNonStream / handleStream / connectStreamWithRetry 三处写入 context |
| `cmd/JoyCodeProxy/serve.go` | 修改 | 中间件从 context 读取 errorDetail 并传入 LogRequest |
| `web/src/api.ts` | 修改 | RequestLog 接口增加 error_detail 字段 |
| `web/src/pages/AccountDetail.tsx` | 修改 | 展开行错误详情区域解析并展示 Descriptions |
| `pkg/store/store_test.go` | 修改 | 测试中 LogRequest 调用适配新签名 |
| `pkg/dashboard/handler_test.go` | 修改 | 测试中 LogRequest 调用适配新签名 |

### 测试结果

- `go test ./pkg/store/...` — PASS
- `go test ./pkg/anthropic/...` — PASS
- `go test ./pkg/dashboard/...` — PASS
- `go test ./cmd/JoyCodeProxy/...` — `TestOpenAPIEndpoints/v1_models` 为已有失败（上游服务不可达），与改动无关
- 前端 `npm run build` — 成功

### 未覆盖的边缘情况

- 如果上游返回的错误 JSON 中没有 `error` 顶层键（如直接返回 `{"code":"..."}`），`parseUpstreamErrorDetail` 会返回空字符串，fallback 到原始 `error_message` 展示
- `content_filter` 检测路径（`connectStreamWithRetry` 第 872-877 行）返回的 `chunkData` 不是标准错误 JSON，解析失败后不会写入 context，但不影响功能