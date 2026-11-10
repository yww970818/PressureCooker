# Automatic Environment Values Implementation Plan

**Goal:** 项目环境和集合环境新增通用自动取值：调用方请求来源 IP、当前毫秒时间戳；同步 HTTP、WS、任务快照与 MCP。

**User decisions:** `client_ip` 取浏览器/调用方请求来源 IP，`profile_update_time` 取当前时间戳（毫秒）。变量名任意，不增加业务专用字段。

**Architecture:** 保持现有字符串声明模型，新增完整规则 `{{$auto.client_ip}}`、`{{$auto.timestamp_ms}}`。先合并有效变量再按操作解析，规则原文持久化；同次操作值一致，新操作重新取值，任务重试使用冻结快照。无法获取 IP 不填默认值。HTTP 传输实际来源为管理端识别的 TCP peer，忽略未受信转发头；MCP 将自身连接识别的 peer 通过有签名的内部元信息传给 manager，不能错误使用内部回环地址。

**Stack:** Existing Go/Vue/Scalar/MCP SDK; no schema migration or new runtime dependency.

## Shared interfaces and ownership

### Backend manager (agent)

- `GET /api/environment-runtime`：现有登录/项目校验后返回 `{client_ip:string,timestamp_ms:string}`。来源不可用 client_ip为空，实际使用IP规则须报明确错误；时间戳为正十进制毫秒字符串，按操作固定。前端只在存在自动规则时调用一次。
- Parser remains existing validation entry or is coherently renamed: accepts both random and the two exact `$auto` rules; malformed/embedded `$auto` declarations reject save/execute. No eval.
- Maintain existing override order and `generation_rule` provenance. Capture request operation context once; pin timestamp across project, explicit properties and collection branches. Snapshot replay preserves frozen values. No caller-authored provenance/context trusted.
- HTTP route receives actual r.RemoteAddr by default. MCP metadata is used only after regular authentication AND a valid platform-admin Bearer request AND protocol.VerifyCallerContext passes. Partial/invalid signed metadata fails closed, not fallback. Unknown signed MCP IP stays unavailable, not manager loopback.
- Backend consumes shared protocol functions below. Strip all three internal caller headers from outgoing HTTP proxy/WS handshake headers.
- Owned manager Go files/tests, manager API discovery/docs. Do not edit frontend or MCP/protocol files.

### Frontend (agent)

- Shared `EnvironmentVariableValue` options values `client_ip` and `timestamp_ms` serialize exact rules above, no length input. Project and collection editors both use it.
- `parseVariableRule` recognizes `{kind:'client_ip'}` / `{kind:'timestamp_ms'}`. Synchronous `resolveVariableValues(values, runtimeContext?)` may receive context; auto rules without required data error, random/fixed backwards compatible.
- Add async operation resolver/provider: authenticated project-bound `api('/environment-runtime')` only when effective definitions include auto rules; use one context per send/connect/snapshot and propagate AbortSignal/project capture. No generated values persisted into authored rows/Scalar environment.
- Scalar beforeRequest must await native SDK hook; WS obtains context before config expansion and generation guard prevents late context after cancel/reconnect from mutating/opening old connections. Do not change message-send behavior.
- Context timestamp/IP returns strings, IP must be literal address, not arbitrary text. New calls after project switch cannot land in new project. No polling or external IP service.
- Owned frontend sources/tests/guide only.

### MCP + shared protocol (agent)

New `execution/protocol/caller_context.go`:

- `SignCallerContext(r *http.Request, ip, token string, at time.Time) error`
- `VerifyCallerContext(r *http.Request, token string, at time.Time) (string,error)`
- Export constants: `CallerIPHeader="X-PressureCooker-Caller-IP"`, `CallerTimeHeader="X-PressureCooker-Caller-Time"`, `CallerSignatureHeader="X-PressureCooker-Caller-Signature"`.
- Signature payload exact UTF-8: `pressurecooker-caller-v1\n` + normalized IP (empty means unavailable) + `\n` + decimal UnixMilli + `\n` + HTTP method + `\n` + r.URL.RequestURI() + `\n` + r.Header.Get("X-Project-ID"). HMAC-SHA256 keyed platform admin token, lowercase hex. Duplicate/partial fields, invalid IP/sig/time or >2min absolute skew reject. IP literal normalized via netip.Unmap. This is source attribution, never replaces normal authorization.
- secureHandler captures actual MCP r.RemoteAddr into private Go context, ignoring caller-supplied forwarding/context headers. managerClient signs per outgoing HTTP request using actual captured IP; if no incoming HTTP source, sign empty IP so it cannot turn into internal loopback. Verify actual SDK context propagation in HTTP/native protocol fixture with an injected RemoteAddr differing from manager peer.
- Add execution local module requirement/replace to mcp-go for shared protocol (manager already depends on execution). No new tool required; tool count remains128. Update environment schema descriptions/docs and adapter version if appropriate.
- Owned execution/protocol caller context file/tests + MCP sources/module/tests/docs only. Never edit execution engine/queue or manager implementations.

## Constraints

- Preserve collaborator modifications, historical formats/data and existing sources. No initial git commit; runtime source hashes instead of mass commit.
- No real business load/long connection, no deployment requested. Use isolated fixtures and targeted tests.
- Parser provenance and context cannot be overridden by ordinary request body values, XFF/Forwarded or cookies.
- No new business defaults or hard-coded behavior for `client_ip`/`profile_update_time` names.

## Steps and verification

- [x] Backend RED→GREEN parser/runtime context, API auth/spoof/IPv4/IPv6/missing/cancel cases, effective merge, task freeze/retry/copy and collection rules; deterministic clock and real isolated SQL where needed.
- [x] Frontend RED→GREEN both editors and operation resolution; HTTP/direct+master, WS cancel/reconnect, source failure/no send, two operations fresh/single operation stable, definitions unchanged; scoped tests/build/browser fixture.
- [x] Protocol+MCP RED→GREEN HMAC vector/tamper/skew/project/path/method/empty source, actual native SDK peer propagation, metadata stripped, new definitions pass writer tools; module tests and compile.
- [x] Root integrate, independent cross-scope review, appropriate affected-module tests, Vue build, no business traffic, source diff and completion receipt.

## Completion evidence

- Local acceptance: `runtime/acceptance/20261109-auto-environment-variables/completion.json`.
- Independent review found duplicate managed XML fields; the six rejection cases, legacy positive control and independent overlays are green after the bounded fix. Phase-one and post-hardening evidence are retained separately.
- No schema migration, deployment or real business traffic. Existing configuration/history was not rewritten.
