# JoyCode 真实客户端行为对齐 — 差异台账

日期: 2026-07-17
状态: 决策依据文档（差异台账）。被选中的项后续各自出实现计划。

## Context

基于 mitmproxy 抓包（`~/Desktop/claude-audit.txt`，真实 JoyCode 插件 vsfront-3.8.63 / VS Code 1.128.0，2026-07-14）分析了 JoyCode 插件的完整上游协议，对比本代理（JoyCode2Api）实现。真实客户端所有 AI 请求走 JD color 网关：

```
POST https://api-ai.jd.com/api?appid=joycode_ide&functionId=<fn>&t=<ms>&sign=<hmac>
鉴权 header: ptKey + loginType + tenant
```

本代理已实现核心对话链路。本文档记录**全部**已核对的差异（含已对齐、已缓解、以及决定暂不做的项），每个差异含：现状（file:line 证据）、真实行为、需要/不需要的理由、改动方向。目的是留一份完整台账，避免以后重复分析、并说明「为什么某些项不做」。

## 已对齐 / 已缓解（无需再做）

### colorBaseUrl 动态刷新 — 已实现
- `pkg/configref/colorbaseurl.go`：`RefreshAccountColorBaseURL` / `RefreshAllAccountsColorBaseURL`。
- `pkg/configref/refresher.go:253`：接入 30min 刷新周期（首启 + 每周期）。
- `pkg/dashboard/handler.go:1163`：验证用户时刷新，结果入响应。
- 运行时空值回退 `joycode.DefaultColorBaseURL`（`client.go:35/130/158-161`；`serve.go:168/178/189` 注入）。

### 核心协议链路 — 已实现
`plugin_config`、`model_strategy_config`（抓取）、`model_error_config_get`、`joycode_modelList`、`joycode_userInfo`、`anthropic_completions` 均已实现（`pkg/joycode/config_api.go`、`pkg/configref/refresher.go`、`pkg/anthropic/handler.go`）。

### 模型列表漂移 — 已缓解（MITIGATED），非功能问题
- 硬编码 `joycode.Models`（`client.go:66-90`，22 项）与真实 `jifei_model`/`joycode_modelList` 目录有出入（缺 `claude-sonnet-4-v1`/`Gemini 3-Pro-Preview`/`GPT-5.1-codex-max`/非 hq 的 `Claude-*` 等）。
- 但 `knownModels()` 优先用 configref 实时抓取并缓存的 `joycode_modelList`（`anthropic/handler.go:50-57`、`openai/handler.go:43-51`），硬编码仅在「冷启动 + 首次 live fetch 失败」窄窗口兜底，且未命中模型回退默认模型而非硬失败（`translate.go`）。
- **结论：不必手动追平硬编码列表。**

## 仍未对齐的差异

### A. `model_runtime_prepare` 未实现 —（正确性 / 抗限流；需决策）
- 现状：completion（`PostAnthropicStream`→`doAnthropicPost`，`client.go:408/324`）直接打 `anthropic_completions`，前面无 prepare。全代码无 `model_runtime_prepare`。
- 真实行为：每次对话（尤其 `-hq` 模型）前先 POST `model_runtime_prepare`，拿回 `{tokenStatus:"READY", token:"mt_ready_bypass...", queuePosition, expireAt}`。
- 理由：抓包显示 **prepare 返回的 token 未回传到 completion**，completion 仍只用 ptKey 鉴权 —— prepare 是「排队/放行探针」，不是必需凭证，当前不做也能直连成功。风险：若上游对 `-hq` 强制要求先 prepare 或据此限流/排队，缺失会偶发拒绝，且拿不到 queuePosition/健康度。属「行为对齐 + 抗限流加固」，非当前阻断项。
- 改动方向：`pkg/joycode` 加 `ModelRuntimePrepare(model, chatId, ...)`（走 `PostByFunctionID("model_runtime_prepare", ...)`）；`anthropic/handler.go` 在发 completion 前对 `-hq` 模型调用，解析 `tokenStatus`/`queuePosition`，READY 才继续，非 READY 按 `model_error_config` 文案提示或短暂重试；token 无需回传。

### B. completion 缺 `x-ms-client-request-id` 头 —（客户端指纹；低成本）
- 现状：`anthropicHeaders()`（`client.go:249-268`）设 8 个头，无任何 `x-ms-*`；代理内部 requestID（`anthropic/logger.go`）从不写出上游。
- 真实行为：completion 带 `x-ms-client-request-id`，值形如 `<taskId>_<sessionId>_<ms>`。
- 理由：不影响功能，属指纹对齐；缺失更易被识别为非官方客户端。
- 改动方向：`anthropicHeaders()` 加 `x-ms-client-request-id`，值用 `<taskId>_<sessionId>_<unixmilli>`；taskId/sessionId 复用 `c.SessionID` 或新生成，毫秒时间戳现取。

### C. 埋点 / 上报类 functionId 全缺 —（指纹 / 使用统计；视目标而定）
- 现状：`telemetry`、`code_generation_metrics`、`log_history`、`notice_delivery`、`person_space`、`team_space_page` 全部 NOT IMPLEMENTED（代码内匹配到的同名字符串均为本地 DB 统计注释，非上游调用）。
- 真实行为：插件在对话前后持续上报（telemetry 事件流、每任务 metrics、每轮 prompt 的 log_history 等）。
- 理由：不影响对话功能。缺失可能导致 (a) 服务端「无使用记录」，(b) 若上游据埋点做配额/风控可能异常，(c) 指纹与官方不一致。**是否做取决于「是否需要贴近官方客户端 / 满足使用统计」目标。**
- 改动方向：`pkg/joycode` 加各 functionId 薄封装（走 `PostByFunctionID`），在对话生命周期节点异步 fire-and-forget（对话前 telemetry `task_init_start`；completion 后 `code_generation_metrics` + `log_history`）；字段参照抓包（osName/ideVersion/pluginVersion/userId/tenant/eventName/traceId 等）；上报失败只记日志、不影响对话。`person_space`/`team_space_page`/`notice_delivery` 作可选启动期调用。**建议分批：先 telemetry+metrics+log_history，后 space/notice。**

### D. `model_strategy_config` 已抓取但未使用 —（未实现的辅助能力，非缺陷）
- 现状：`FetchModelStrategyConfig`（`config_api.go:81`）抓到后仅存 `strategy_config`（`refresher.go:237-239`）；**无 `GetRemoteConfig("strategy_config")` 读取**，autoModel 未用于路由。
- 真实用途：把 `llm-commit`/`llm-prompt`/`llm-cli`/`llm-auto1` 内置任务路由到对应 autoModel。
- 理由：本代理是纯对话代理，不做 commit-message 生成等辅助功能，故属未实现的辅助能力，非缺陷。
- 改动方向（仅当扩展辅助功能时）：加 `GetRemoteConfig("strategy_config")` 读取 + 按 `taskType` 查 autoModel 的路由函数。**当前不做。**

### E. `plugin_config` 少 `ihub_rule_url` —（极低成本对齐）
- 现状：`pluginConfigSceneTypes`（`refresher.go:157-167`）含 9 个 sceneType，缺 `ihub_rule_url`。
- 理由：仅少抓一个配置项，几乎无功能影响，补上是一行。
- 改动方向：`pluginConfigSceneTypes` 追加 `"ihub_rule_url"`。

### F. `isWhite2Commit` / `loginResultCheck` / `knowledge/v1/datasets` 未实现 —（辅助功能）
- 现状：均未实现。
- 真实用途：commit 白名单门控、登录态校验、RAG 知识库列表。
- 理由：对纯 API 代理非必需。
- 改动方向：暂无——列为「已知未实现的辅助能力」，仅在扩展 commit / RAG 功能时再评估。**当前不做。**

## 优先级建议（供决策，非执行顺序）

1. **A（model_runtime_prepare）** — 唯一可能影响 `-hq` 稳定性/被限流的项，权重最高；当前不阻断。
2. **B + E** — 低成本对齐（各约 1~几行），随手可做。
3. **C（埋点全家桶）** — 中等工作量，仅当目标是「像官方客户端 / 需要使用统计」时才做。
4. **D、F** — 仅在扩展辅助功能时涉及，当前不做。

## Verification（各项做完后如何验证）

- **A**：`-hq` 模型（如 `Claude-Opus-4.8-hq`）跑一次真实对话，抓包/日志确认 completion 前有 `model_runtime_prepare` 且 `tokenStatus:READY`；高频请求下观察是否不再偶发限流。
- **B**：抓代理发往上游的 completion 请求头，确认含 `x-ms-client-request-id`。
- **C**：对话前后抓包确认对应 functionId 被发送且上游返回 `code:0`。
- **E**：确认 configref 一轮刷新后 store 出现 `plugin_config_ihub_rule_url`。
- 通用：`go test ./pkg/...` 全绿；`./dev.sh` 启动后用真实 ERP 账号跑一次对话验证不回归。

## 结论

当前**没有阻断功能的差异**。真正影响稳定性的候选只有 **A（model_runtime_prepare）**；B/C/E 是不同程度的行为/指纹对齐；D/F 是未涉及的辅助能力。建议先明确目标（仅要稳定可用 vs. 尽量贴近官方客户端），再决定做 A、A+B+E、还是连 C。
