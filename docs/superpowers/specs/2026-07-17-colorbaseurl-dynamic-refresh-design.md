# colorBaseUrl 动态刷新设计

日期: 2026-07-17

## 背景

每个账号的上游 AI 网关地址 `colorBaseUrl` 由 JD `joycode_userInfo` 接口在登录时返回。当前实现只在**加账号时取一次**（`validateAndSavePtKey` → `UserInfo` → 存入 `AccountCreds.ColorBaseURL`），之后固定不变。若上游迁移地址或换租户，写死的旧值会导致请求发往错误地址。

抓包（`~/Desktop/claude-audit.txt`，插件 vsfront-3.8.63）确认：真实客户端登录时从 `joycode_userInfo` 拿 `colorBaseUrl` / `masterBaseUrl` / `slaveBaseUrl` 作为路由表。本项目仅用到 `colorBaseUrl`（AI 网关，占抓包 99% 请求）；master/slave 是业务库地址，本项目未使用，本次不处理。

## 目标

让每个账号的 `colorBaseUrl` 持续跟随上游动态更新，在三个时机刷新，失败可见，失败时回退硬编码默认值。

- 硬编码默认值: `https://api-ai.jd.com`（`joycode.DefaultColorBaseURL`）
- 刷新范围: **所有账号，各自刷新自己的 colorBaseUrl**
- 定时周期: **跟随现有 configref 刷新器的 30 分钟周期**（合并进现有刷新器，不另开 24h 独立循环）
- 失败展示: **按账号**展示上次刷新时间与失败错误

## 数据模型改动（pkg/store）

`accounts` 表新增两列（`ALTER TABLE ... ADD COLUMN`，幂等，跟随现有迁移风格）:

- `color_refresh_at` TEXT — 上次尝试刷新 colorBaseUrl 的时间（RFC3339）
- `color_refresh_error` TEXT — 上次失败的错误信息；成功时清空为空串

相应改动:
- `Account`、`AccountInfo`、export 结构体加 `ColorRefreshAt` / `ColorRefreshError` 字段（json: `color_refresh_at` / `color_refresh_error`）
- 所有 `rows.Scan(...)` / `QueryRow(...).Scan(...)` 读取账号的位置补上两列
- 新增 `UpdateAccountColorStatus(userID, refreshAt, refreshErr string) error`（只更新这两列，避免和 `UpdateAccountCreds` 的 creds 回写耦合）

## 核心逻辑：共享刷新函数

新增函数（放在 configref 包，供刷新器与 dashboard 复用；接收 `*store.Store` 与账号 userID）:

```
RefreshAccountColorBaseURL(s *store.Store, userID string) (colorBaseURL string, err error)
```

流程:
1. 取账号 → 用 ptKey 建 `joycode.NewClient` → 调 `UserInfo()`
2. **成功**且 `data.colorBaseUrl` 非空:
   - `UpdateAccountCreds` 回写 colorBaseUrl（顺带 masterBaseUrl / tenant / orgFullName，若非空）
   - `UpdateAccountColorStatus(userID, now, "")`
3. **失败**（请求错误 / code≠0 / colorBaseUrl 为空）:
   - **不修改**已存的 colorBaseUrl
   - `UpdateAccountColorStatus(userID, now, <错误原因>)`
4. 运行时建 client 时（`serve.go:168/178` 已有 `SetColorContext`），若账号 colorBaseUrl 为空，`SetColorContext("", ...)` 天然保持 `joycode.DefaultColorBaseURL`。**fallback 已由 SetColorContext 语义满足，无需额外分支**。

## 三个触发时机

1. **首次启动**: `configref` 首轮 `refresh()` 末尾遍历所有账号调 `RefreshAccountColorBaseURL`。（`serve.go` 已在 `Start` 里 `go r.refresh()`。）
2. **定时**: 合并进现有 30 分钟 config 刷新循环——每轮 config 刷新后顺带刷新所有账号 colorBaseUrl。单账号失败不影响其他账号，也不影响 config 主流程（各自 try/log）。
3. **验证用户**: `dashboard.validateAccount`（handler.go:1154）在 `client.Validate()` 之后，对该账号调 `RefreshAccountColorBaseURL`，结果一并回写。验证响应体带上刷新后的 colorBaseUrl 与失败信息（若有）。

## 失败状态展示（按账号）

- 后端: `/api/accounts` 列表项与 `/api/accounts-export` 带上 `color_refresh_at`、`color_refresh_error`
- 前端:
  - `web/src/api.ts` 账号类型加 `color_refresh_at?` / `color_refresh_error?`
  - `Accounts.tsx` / `AccountDetail.tsx` 账号项: 成功时不显眼；失败时红字「上次获取 colorBaseUrl 失败: <error>（<时间>）」

## 错误处理与边界

- 单账号刷新失败隔离，不影响其他账号与 config 主刷新
- colorBaseUrl 为空 → 运行时回退默认值，对话不中断
- 无账号 / ptKey 为空 → 跳过，不报错

## 测试

- store: 新列迁移 + `UpdateAccountColorStatus` / Scan 往返
- `RefreshAccountColorBaseURL`: mock `UserInfo`
  - 返回新 URL → 回写 + 清空 error
  - 返回空 colorBaseUrl 或 code≠0 或请求错误 → 保留旧值 + 记录 error + 记录时间
- `validateAccount`: 成功刷新回写；失败保留旧值并返回错误信息

## 非目标

- 不处理 masterBaseUrl / slaveBaseUrl 的动态使用（本项目未用到业务库）
- 不新增独立 24h 定时循环（跟随现有 30min 周期）
- 不改动 `model_runtime_prepare` 等其他协议差异（另议）
