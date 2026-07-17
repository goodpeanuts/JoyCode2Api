# colorBaseUrl 动态刷新 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每个账号的 `colorBaseUrl` 在首次启动、每 30 分钟 config 刷新周期、以及点击「验证用户」时从 `joycode_userInfo` 动态刷新，失败按账号展示于配置界面，失败时运行时回退硬编码默认值。

**Architecture:** 在 `accounts` 表新增两列记录按账号的 colorBaseUrl 刷新状态；新增一个共享刷新函数 `configref.RefreshAccountColorBaseURL`，被 config 刷新循环（所有账号）与 dashboard 的 validateAccount（单账号）复用；成功回写 `UpdateAccountCreds`，失败保留旧值仅记状态。前端账号类型加两字段并在账号项展示失败。

**Tech Stack:** Go 1.x（标准库 database/sql + SQLite）、现有 `pkg/store` / `pkg/configref` / `pkg/joycode` / `pkg/dashboard`、React + TypeScript（`web/src`）。

## Global Constraints

- 硬编码默认 colorBaseUrl: `https://api-ai.jd.com`（常量 `joycode.DefaultColorBaseURL`，client.go:35）
- 迁移用幂等 `ALTER TABLE ... ADD COLUMN ... DEFAULT ''`（跟随 store.go:304-318 现有风格），不新建表
- 时间格式: 存储用 `time.Now().UTC().Format(time.RFC3339)`（保持字段为可读字符串，前端 `new Date(...)` 可解析）
- 定时周期跟随现有 configref 的 30min（serve.go:100），不新增独立 24h 循环
- 刷新范围: 所有账号，各自刷新自己的 colorBaseUrl
- 失败隔离: 单账号刷新失败不 return，不影响其他账号或 config 主流程；用 slog.Warn 记录
- 空 colorBaseUrl 的运行时回退由 `SetColorContext("", ...)` 语义天然满足（serve.go:168/178），本计划不改运行时注入代码

---

### Task 1: store 新增按账号 colorBaseUrl 刷新状态列与更新方法

**Files:**
- Modify: `pkg/store/store.go`（迁移块 ~318、`AccountInfo` struct ~66、`ListAccounts` SELECT+Scan ~870/881、新增方法在 `UpdateAccountCreds` ~1256 之后）
- Test: `pkg/store/store_color_status_test.go`（Create）

**Interfaces:**
- Produces:
  - `AccountInfo.ColorRefreshAt string`（json `color_refresh_at`）
  - `AccountInfo.ColorRefreshError string`（json `color_refresh_error`）
  - `func (s *Store) UpdateAccountColorStatus(userID, refreshAt, refreshErr string) error` — 只更新 `color_refresh_at` / `color_refresh_error` 两列
  - `func (s *Store) GetAccountColorStatus(userID string) (refreshAt, refreshErr string, err error)` — 测试用读取

- [ ] **Step 1: 写失败测试**

Create `pkg/store/store_color_status_test.go`:

Note: the store package already has a `openTestStore(t)` helper (store_test.go:10) that opens a temp-file DB. Reuse it — do NOT add a new helper.

```go
package store

import (
	"testing"
)

func TestUpdateAccountColorStatus(t *testing.T) {
	s := openTestStore(t)
	creds := &AccountCreds{ColorBaseURL: "https://api-ai.jd.com"}
	if err := s.AddAccount("u1", "ptkey-1", "nick", true, "GLM-5.1", creds); err != nil {
		t.Fatalf("add account: %v", err)
	}

	if err := s.UpdateAccountColorStatus("u1", "2026-07-17T00:00:00Z", "boom"); err != nil {
		t.Fatalf("update color status: %v", err)
	}
	at, errMsg, err := s.GetAccountColorStatus("u1")
	if err != nil {
		t.Fatalf("get color status: %v", err)
	}
	if at != "2026-07-17T00:00:00Z" || errMsg != "boom" {
		t.Fatalf("got at=%q err=%q, want at=2026-07-17T00:00:00Z err=boom", at, errMsg)
	}

	// success path clears error
	if err := s.UpdateAccountColorStatus("u1", "2026-07-17T01:00:00Z", ""); err != nil {
		t.Fatalf("update color status 2: %v", err)
	}
	at, errMsg, _ = s.GetAccountColorStatus("u1")
	if at != "2026-07-17T01:00:00Z" || errMsg != "" {
		t.Fatalf("got at=%q err=%q, want cleared error", at, errMsg)
	}
}

func TestListAccountsIncludesColorStatus(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddAccount("u2", "ptkey-2", "nick2", true, "GLM-5.1", &AccountCreds{}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	if err := s.UpdateAccountColorStatus("u2", "2026-07-17T02:00:00Z", "failed-x"); err != nil {
		t.Fatalf("update: %v", err)
	}
	accounts, err := s.ListAccounts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, a := range accounts {
		if a.UserID == "u2" {
			found = true
			if a.ColorRefreshAt != "2026-07-17T02:00:00Z" || a.ColorRefreshError != "failed-x" {
				t.Fatalf("got at=%q err=%q", a.ColorRefreshAt, a.ColorRefreshError)
			}
		}
	}
	if !found {
		t.Fatal("u2 not in list")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/store/ -run 'TestUpdateAccountColorStatus|TestListAccountsIncludesColorStatus' -v`
Expected: 编译失败或 FAIL —— `UpdateAccountColorStatus` / `GetAccountColorStatus` / `ColorRefreshAt` 未定义。

（若 `New` 签名或测试 helper 与现有测试不符，先看 `pkg/store/store_test.go` 里现有 `New(...)` 调用方式并对齐，再改本测试的 helper。）

- [ ] **Step 3: 加迁移列**

在 `pkg/store/store.go` 迁移块（store.go:318 `org_full_name` 那行之后）追加:

```go
	// Migration: add per-account colorBaseUrl refresh status
	s.db.Exec("ALTER TABLE accounts ADD COLUMN color_refresh_at TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE accounts ADD COLUMN color_refresh_error TEXT DEFAULT ''")
```

- [ ] **Step 4: AccountInfo 加字段**

在 `AccountInfo` struct（store.go:88 `OrgFullName` 之后）追加:

```go
	ColorRefreshAt    string `json:"color_refresh_at,omitempty"`
	ColorRefreshError string `json:"color_refresh_error,omitempty"`
```

- [ ] **Step 5: ListAccounts SELECT + Scan 补两列**

在 store.go:870，把 SELECT 末尾 `COALESCE(org_full_name,'')` 改为追加两列:

```go
	rows, err := s.db.Query("SELECT user_id, nickname, remark, api_token, is_default, default_model, created_at, credential_valid, credential_refreshed_at, COALESCE(display_order, 0), COALESCE(login_type,''), COALESCE(tenant,''), COALESCE(color_base_url,''), COALESCE(master_base_url,''), COALESCE(org_full_name,''), COALESCE(color_refresh_at,''), COALESCE(color_refresh_error,'') FROM accounts ORDER BY display_order, created_at")
```

对应 Scan（store.go:881）末尾追加两个字段:

```go
		if err := rows.Scan(&a.UserID, &a.Nickname, &a.Remark, &a.APIToken, &isDef, &a.DefaultModel, &a.CreatedAt, &a.CredentialValid, &a.CredentialRefreshAt, &a.DisplayOrder, &a.LoginType, &a.Tenant, &a.ColorBaseURL, &a.MasterBaseURL, &a.OrgFullName, &a.ColorRefreshAt, &a.ColorRefreshError); err != nil {
```

- [ ] **Step 6: 新增 Update / Get 方法**

在 `UpdateAccountCreds`（store.go:1256）之后追加:

```go
// UpdateAccountColorStatus records the outcome of a colorBaseUrl refresh attempt
// for an account. refreshErr is empty on success (which clears any prior error).
func (s *Store) UpdateAccountColorStatus(userID, refreshAt, refreshErr string) error {
	_, err := s.db.Exec(
		"UPDATE accounts SET color_refresh_at = ?, color_refresh_error = ? WHERE user_id = ?",
		refreshAt, refreshErr, userID,
	)
	if err != nil {
		slog.Error("store: update account color status failed", "user_id", userID, "error", err)
	}
	return err
}

// GetAccountColorStatus returns the last colorBaseUrl refresh time and error for an account.
func (s *Store) GetAccountColorStatus(userID string) (refreshAt, refreshErr string, err error) {
	err = s.db.QueryRow(
		"SELECT COALESCE(color_refresh_at,''), COALESCE(color_refresh_error,'') FROM accounts WHERE user_id = ?",
		userID,
	).Scan(&refreshAt, &refreshErr)
	return refreshAt, refreshErr, err
}
```

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./pkg/store/ -run 'TestUpdateAccountColorStatus|TestListAccountsIncludesColorStatus' -v`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add pkg/store/store.go pkg/store/store_color_status_test.go
git commit -m "feat(store): per-account colorBaseUrl refresh status columns"
```

---

### Task 2: configref 共享刷新函数 RefreshAccountColorBaseURL

**Files:**
- Create: `pkg/configref/colorbaseurl.go`
- Test: `pkg/configref/colorbaseurl_test.go`

**Interfaces:**
- Consumes: `store.UpdateAccountCreds`、`store.UpdateAccountColorStatus`、`store.GetAccount`、`joycode.NewClient`、`joycode.DefaultColorBaseURL`
- Produces:
  - `func RefreshAccountColorBaseURL(s *store.Store, userID string) (colorBaseURL string, err error)` — 用账号 ptKey 调 UserInfo；成功回写 colorBaseUrl + 清 error，返回新 colorBaseUrl；失败保留旧值 + 记 error，返回 `("", err)`
  - `func RefreshAllAccountsColorBaseURL(s *store.Store)` — 遍历所有账号各自调上者，单个失败仅 slog.Warn

**注意（依赖注入以便测试）：** `joycode.Client` 已有 `SetHTTPClient(*http.Client)`（client.go:140）。为让 `RefreshAccountColorBaseURL` 可测，函数内部构造 client 后，若存在包级可替换钩子则用之。实现用一个包级变量 `newUserInfoClient` 便于测试替换（见下）。

- [ ] **Step 1: 写失败测试**

Create `pkg/configref/colorbaseurl_test.go`:

```go
package configref

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

func newColorTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// mockUserInfoServer returns a test server that replies with the given colorBaseUrl.
func mockUserInfoServer(t *testing.T, colorBaseURL string, code int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if code != 0 {
			w.Write([]byte(`{"code":999,"msg":"boom","data":null}`))
			return
		}
		w.Write([]byte(`{"code":0,"msg":"ok","data":{"userId":"u1","colorBaseUrl":"` + colorBaseURL + `"}}`))
	}))
}

func TestRefreshAccountColorBaseURL_Success(t *testing.T) {
	s := newColorTestStore(t)
	if err := s.AddAccount("u1", "ptkey", "nick", true, "GLM-5.1", &store.AccountCreds{ColorBaseURL: "https://old.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srv := mockUserInfoServer(t, "https://new.example.com", 0)
	defer srv.Close()

	// Route the client's requests to the mock by overriding the UserInfo endpoint origin.
	origNew := newUserInfoClient
	newUserInfoClient = func(ptKey, userID string) *joycode.Client {
		c := joycode.NewClient(ptKey, userID)
		c.ColorBaseURL = srv.URL // color gateway origin -> mock
		return c
	}
	defer func() { newUserInfoClient = origNew }()

	url, err := RefreshAccountColorBaseURL(s, "u1")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if url != "https://new.example.com" {
		t.Fatalf("got %q, want https://new.example.com", url)
	}
	acc, _ := s.GetAccount("u1")
	if acc.ColorBaseURL != "https://new.example.com" {
		t.Fatalf("db colorBaseUrl = %q, want new", acc.ColorBaseURL)
	}
	at, errMsg, _ := s.GetAccountColorStatus("u1")
	if at == "" || errMsg != "" {
		t.Fatalf("status at=%q err=%q, want time set + no error", at, errMsg)
	}
}

func TestRefreshAccountColorBaseURL_FailureKeepsOldValue(t *testing.T) {
	s := newColorTestStore(t)
	if err := s.AddAccount("u1", "ptkey", "nick", true, "GLM-5.1", &store.AccountCreds{ColorBaseURL: "https://old.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srv := mockUserInfoServer(t, "", 999)
	defer srv.Close()
	origNew := newUserInfoClient
	newUserInfoClient = func(ptKey, userID string) *joycode.Client {
		c := joycode.NewClient(ptKey, userID)
		c.ColorBaseURL = srv.URL
		return c
	}
	defer func() { newUserInfoClient = origNew }()

	url, err := RefreshAccountColorBaseURL(s, "u1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if url != "" {
		t.Fatalf("got url %q, want empty on failure", url)
	}
	acc, _ := s.GetAccount("u1")
	if acc.ColorBaseURL != "https://old.example.com" {
		t.Fatalf("old value not preserved: %q", acc.ColorBaseURL)
	}
	_, errMsg, _ := s.GetAccountColorStatus("u1")
	if errMsg == "" {
		t.Fatal("expected recorded error, got empty")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/configref/ -run TestRefreshAccountColorBaseURL -v`
Expected: 编译失败 —— `RefreshAccountColorBaseURL` / `newUserInfoClient` 未定义。

- [ ] **Step 3: 实现**

Create `pkg/configref/colorbaseurl.go`:

```go
package configref

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

// newUserInfoClient builds the joycode client used for colorBaseUrl refresh.
// It is a package var so tests can point the client at a mock server.
var newUserInfoClient = func(ptKey, userID string) *joycode.Client {
	return joycode.NewClient(ptKey, userID)
}

// RefreshAccountColorBaseURL calls joycode_userInfo with the account's ptKey and,
// on success, writes the returned colorBaseUrl (plus master/tenant/orgFullName when
// present) back to the account. On any failure the stored colorBaseUrl is left
// unchanged. Either way the per-account color refresh status (time + error) is updated.
// Returns the new colorBaseUrl on success, or ("", err) on failure.
func RefreshAccountColorBaseURL(s *store.Store, userID string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	acc, err := s.GetAccount(userID)
	if err != nil || acc == nil {
		e := fmt.Errorf("account %q not found: %w", userID, err)
		s.UpdateAccountColorStatus(userID, now, e.Error())
		return "", e
	}

	client := newUserInfoClient(acc.PtKey, acc.UserID)
	resp, err := client.UserInfo()
	if err != nil {
		s.UpdateAccountColorStatus(userID, now, err.Error())
		return "", err
	}
	code, _ := resp["code"].(float64)
	if code != 0 {
		msg, _ := resp["msg"].(string)
		e := fmt.Errorf("userInfo error (code=%.0f): %s", code, msg)
		s.UpdateAccountColorStatus(userID, now, e.Error())
		return "", e
	}
	data, _ := resp["data"].(map[string]interface{})
	colorBaseURL, _ := data["colorBaseUrl"].(string)
	if colorBaseURL == "" {
		e := fmt.Errorf("userInfo returned empty colorBaseUrl")
		s.UpdateAccountColorStatus(userID, now, e.Error())
		return "", e
	}

	creds := &store.AccountCreds{ColorBaseURL: colorBaseURL}
	if v, ok := data["masterBaseUrl"].(string); ok {
		creds.MasterBaseURL = v
	}
	if v, ok := data["tenant"].(string); ok {
		creds.Tenant = v
	}
	if v, ok := data["orgFullName"].(string); ok {
		creds.OrgFullName = v
	}
	if err := s.UpdateAccountCreds(userID, creds); err != nil {
		s.UpdateAccountColorStatus(userID, now, err.Error())
		return "", err
	}
	s.UpdateAccountColorStatus(userID, now, "")
	return colorBaseURL, nil
}

// RefreshAllAccountsColorBaseURL refreshes colorBaseUrl for every account.
// A single account's failure is logged and does not stop the others.
func RefreshAllAccountsColorBaseURL(s *store.Store) {
	accounts, err := s.ListAccounts()
	if err != nil {
		slog.Warn("configref: list accounts for color refresh failed", "error", err)
		return
	}
	for _, a := range accounts {
		if _, err := RefreshAccountColorBaseURL(s, a.UserID); err != nil {
			slog.Warn("configref: colorBaseUrl refresh failed", "user_id", a.UserID, "error", err)
		}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./pkg/configref/ -run TestRefreshAccountColorBaseURL -v`
Expected: PASS

（若 `GetAccount` 返回的 `acc.PtKey` 为空导致 UserInfo 走真实网络：测试已用 `newUserInfoClient` 覆盖，client 的 `ColorBaseURL` 指向 mock，故不会打真实上游。确认 `joycode.Client.UserInfo` 走的是 `ColorBaseURL`——见 client.go requestURL/colorEndpoints 的 `joycode_userInfo` 映射。）

- [ ] **Step 5: 提交**

```bash
git add pkg/configref/colorbaseurl.go pkg/configref/colorbaseurl_test.go
git commit -m "feat(configref): RefreshAccountColorBaseURL shared refresh function"
```

---

### Task 3: 接入 config 刷新周期（首次启动 + 30min）

**Files:**
- Modify: `pkg/configref/refresher.go`（`refreshWithUser` 末尾，~260 `setStatus` 调用之前）
- Test: `pkg/configref/colorbaseurl_test.go`（追加一个集成向测试）

**Interfaces:**
- Consumes: `RefreshAllAccountsColorBaseURL`（Task 2）

- [ ] **Step 1: 写测试（验证 refresh 周期会刷新账号 colorBaseUrl）**

在 `pkg/configref/colorbaseurl_test.go` 追加:

```go
func TestRefreshCycleRefreshesColorBaseURL(t *testing.T) {
	s := newColorTestStore(t)
	if err := s.AddAccount("u1", "ptkey", "nick", true, "GLM-5.1", &store.AccountCreds{ColorBaseURL: "https://old.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srv := mockUserInfoServer(t, "https://cycle.example.com", 0)
	defer srv.Close()
	origNew := newUserInfoClient
	newUserInfoClient = func(ptKey, userID string) *joycode.Client {
		c := joycode.NewClient(ptKey, userID)
		c.ColorBaseURL = srv.URL
		return c
	}
	defer func() { newUserInfoClient = origNew }()

	// Directly exercise the all-accounts helper the cycle calls.
	RefreshAllAccountsColorBaseURL(s)

	acc, _ := s.GetAccount("u1")
	if acc.ColorBaseURL != "https://cycle.example.com" {
		t.Fatalf("colorBaseUrl not refreshed by cycle helper: %q", acc.ColorBaseURL)
	}
}
```

- [ ] **Step 2: 运行确认通过（helper 已存在，应直接 PASS）**

Run: `go test ./pkg/configref/ -run TestRefreshCycleRefreshesColorBaseURL -v`
Expected: PASS（此测试锁定行为，防止后续回归）

- [ ] **Step 3: 在 refresh 周期末尾接入**

在 `pkg/configref/refresher.go` 的 `refreshWithUser` 里，第 4 步「Fetch error config」之后、`if firstErr != nil {` 之前（refresher.go:249 与 251 之间）插入:

```go
	// 5. Refresh each account's colorBaseUrl from joycode_userInfo.
	//    Runs on the same cycle as config refresh (first run at startup + every interval).
	RefreshAllAccountsColorBaseURL(r.store)
```

- [ ] **Step 4: 全包测试**

Run: `go test ./pkg/configref/ -v`
Expected: PASS（全部）

- [ ] **Step 5: 提交**

```bash
git add pkg/configref/refresher.go pkg/configref/colorbaseurl_test.go
git commit -m "feat(configref): refresh account colorBaseUrl on config refresh cycle"
```

---

### Task 4: 验证用户时刷新 colorBaseUrl

**Files:**
- Modify: `pkg/dashboard/handler.go`（`validateAccount` ~1142-1162）

**Interfaces:**
- Consumes: `configref.RefreshAccountColorBaseURL`（Task 2）
- Produces: validate 响应体新增 `color_base_url`（成功时新值）与 `color_refresh_error`（失败原因，若有）

- [ ] **Step 1: 改写 validateAccount**

把 `pkg/dashboard/handler.go` 的 `validateAccount`（1142-1162）改为在 Validate 之后刷新 colorBaseUrl:

```go
func (h *Handler) validateAccount(w http.ResponseWriter, r *http.Request, apiKey string) {
	account, err := h.store.GetAccount(apiKey)
	if err != nil {
		slog.Error("get account", "api_key", apiKey, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if account == nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	client := joycode.NewClient(account.PtKey, account.UserID)
	valid := true
	if err := client.Validate(); err != nil {
		valid = false
		slog.Error("validate account", "api_key", apiKey, "error", err)
	}

	// Also refresh colorBaseUrl on validate. Failure keeps the old value;
	// the error is surfaced in the response and recorded per-account.
	colorBaseURL, colorErr := configref.RefreshAccountColorBaseURL(h.store, account.UserID)
	colorErrMsg := ""
	if colorErr != nil {
		colorErrMsg = colorErr.Error()
		slog.Warn("validate: colorBaseUrl refresh failed", "api_key", apiKey, "error", colorErr)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"api_key":            apiKey,
		"valid":              valid,
		"color_base_url":     colorBaseURL,
		"color_refresh_error": colorErrMsg,
	})
}
```

- [ ] **Step 2: 确认 configref 已 import**

检查 `pkg/dashboard/handler.go` 顶部 import 是否已含 `"github.com/vibe-coding-labs/JoyCodeProxy/pkg/configref"`（handler.go:25 已 import，无需改）。若缺失则补上。

- [ ] **Step 3: 编译 + 现有 dashboard 测试**

Run: `go build ./... && go test ./pkg/dashboard/ -v`
Expected: 编译通过；现有测试 PASS。

- [ ] **Step 4: 提交**

```bash
git add pkg/dashboard/handler.go
git commit -m "feat(dashboard): refresh colorBaseUrl when validating an account"
```

---

### Task 5: 前端账号类型加字段并展示失败

**Files:**
- Modify: `web/src/api.ts`（accounts 相关类型 ~271-272 与账号 interface）
- Modify: `web/src/pages/Accounts.tsx`（账号项渲染）

**Interfaces:**
- Consumes: `/api/accounts` 响应中账号项新增的 `color_refresh_at?` / `color_refresh_error?`

- [ ] **Step 1: 定位账号类型**

Run: `grep -n "credential_error\|credential_valid\|color_base_url\|interface Account\|type Account" web/src/api.ts`
Expected: 找到前端账号项类型定义处（含 `credential_valid` / `color_base_url` 字段的 interface）。

- [ ] **Step 2: 加两字段**

在该账号 interface 里（`color_base_url?` 附近）追加:

```ts
  color_refresh_at?: string;
  color_refresh_error?: string;
```

- [ ] **Step 3: 定位账号渲染处**

Run: `grep -n "credential_error\|凭据\|无效\|color_base_url\|account\." web/src/pages/Accounts.tsx | head`
Expected: 找到渲染单个账号状态/错误的 JSX 区域。

- [ ] **Step 4: 加失败展示**

在账号项状态区域，仿照现有 `credential_error` 的红字提示，追加（`acc` 为当前账号变量名，按实际替换）:

```tsx
{acc.color_refresh_error && (
  <div style={{ color: '#d33', fontSize: 12 }}>
    上次获取 colorBaseUrl 失败：{acc.color_refresh_error}
    {acc.color_refresh_at && `（${new Date(acc.color_refresh_at).toLocaleString()}）`}
  </div>
)}
```

（若 `Accounts.tsx` 用 CSS class 而非 inline style，则改用与相邻 `credential_error` 提示相同的 class，保持一致。）

- [ ] **Step 5: 前端构建**

Run: `cd web && npm run build`
Expected: 构建成功，无 TypeScript 错误。

- [ ] **Step 6: 提交**

```bash
git add web/src/api.ts web/src/pages/Accounts.tsx
git commit -m "feat(web): show per-account colorBaseUrl refresh failure"
```

---

### Task 6: 端到端手动验证

**Files:** 无（仅运行验证）

- [ ] **Step 1: 全量测试 + 构建**

Run: `go test ./... && go build ./... && (cd web && npm run build)`
Expected: 全部 PASS，二进制与前端构建成功。

- [ ] **Step 2: 启动并观察首次刷新**

Run: `./dev.sh`（或现有启动脚本）后，打开 dashboard 配置/账号页，确认:
- 至少一个账号存在时，日志出现 `configref: refresh` 且无 colorBaseUrl panic
- 账号项在刷新成功时不显红字；构造一个坏 ptKey 账号时显示「上次获取 colorBaseUrl 失败…（时间）」

- [ ] **Step 3: 验证「验证用户」按钮**

在账号页点「验证用户」，确认响应/界面反映 colorBaseUrl 刷新结果（成功刷新新值，或失败保留旧值并提示）。

---

## Self-Review

**Spec coverage:**
- 首次启动获取 → Task 3 Step 3（refresh 周期首轮，serve.go 已 `go r.refresh()`）✓
- 30min 定时（跟随现有周期，非 24h）→ Task 3 ✓
- 失败按账号显示于配置界面（时间 + 错误）→ Task 1（列）+ Task 5（展示）✓
- 验证用户时也刷新 → Task 4 ✓
- 失败保留旧值 → Task 2 实现 + 测试 ✓
- colorBaseUrl 提取失败 fallback 默认值 → Global Constraints（`SetColorContext("")` 语义，serve.go 已具备）✓

**Placeholder scan:** 无 TBD/TODO；所有代码步骤含完整代码。Task 5 因前端确切变量名/样式需读文件确认，已用 grep 定位步骤 + 明确 fallback 指示替代猜测。

**Type consistency:** `UpdateAccountColorStatus(userID, refreshAt, refreshErr string)`、`GetAccountColorStatus` 返回 `(refreshAt, refreshErr, err)`、`RefreshAccountColorBaseURL(s, userID) (string, error)`、`RefreshAllAccountsColorBaseURL(s)`、字段 `ColorRefreshAt`/`ColorRefreshError`（json `color_refresh_at`/`color_refresh_error`）—— 跨 Task 1/2/3/4/5 一致。
