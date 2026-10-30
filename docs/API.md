# 接口约定

本文是管理 API 的唯一契约来源，适用于 Go 服务端（`internal/app`、`internal/run`、`internal/syncer`、`internal/mirror`、`internal/notification`）与 Vue 管理台（`web/src/app/api.ts`、`web/src/app/error-text.ts`、`web/src/app/config-errors.ts`）。通用规则见 [代码规范.md](../代码规范.md) 第 5 节。

服务端只返回英文说明和代码；管理台按代码显示中文，不展示服务端的英文说明。唯一例外是 `details.detail`：它是 rsync、dnf、GitHub 或文件系统等外部工具的原始诊断，管理台把它附在中文说明之后。

## 通用约定

- `GET /healthz` 无需登录，返回 `{"status": "ok"}`；服务关闭中返回 503 `unavailable`。`GET /api/info` 无需登录，返回 `{"version", "capabilities"}`，能力为 `analytics`、`github_release_sources`、`notifications`、`reposync_sources`。
- 其余 `/api/` 接口需要会话 Cookie（`mirrordock_session`，`HttpOnly`、`SameSite=Strict`，HTTPS 时 `Secure`）；写请求经 `Sec-Fetch-Site` / `Origin` 跨站校验。
- 每个响应带 `X-Request-ID`（入站值匹配 `^[A-Za-z0-9-]{1,64}$` 时沿用），与服务端日志的 `request_id` 相同；`/api/` 响应带 `Cache-Control: no-store`。
- 请求体必须是 `application/json`（否则 415），不超过 1 MiB（否则 413），未知字段返回 400。查询参数与路径参数格式错误返回 400 `invalid_argument`。
- 字段与枚举为 snake_case；时间为 UTC 毫秒精度 RFC 3339（`2026-10-23T08:15:30.000Z`）；数值字段名带单位（`elapsed_ms`、`budget_ms`、`eta_ms`（整数毫秒）、`transferred_bytes`、`speed_bytes_per_second`、`next_offset_bytes`）；可选字段缺省时省略。
- 列表为 `{"items": [...], "total": N}`，以 `limit`（1–100，默认 50）与 `offset` 分页：运行记录与通知记录页面需要跳页与总数。`GET /api/runs?latest=true` 返回每个源的最新任务，不分页。
- 配置与通知设置写在同一个 YAML 文件中，以文件 revision 做乐观并发：`GET /api/config`、`GET /api/notifications` 及其写操作的响应带 `ETag`；`PUT /api/config` 与 `PATCH /api/notifications/{channel}` 必须带 `If-Match`，缺失或不是当前 revision 时返回 412 `precondition_failed`。
- `POST /api/sources/{id}/runs` 接受 `Idempotency-Key`（1–255 个可见 ASCII 字符）：24 小时内同一键返回首次的状态码与响应体；同一键但请求不同返回 409 `idempotency_key_reused`。5xx 与 429 不保存，可用同一键重试。
- 没有响应体的成功操作返回 204：`POST /api/auth/logout`、`POST /api/runs/{id}/{action}`、`POST /api/notifications/{channel}/test`、`PUT /api/notifications/timezone`。

## 接口

| 方法与路径 | 说明 |
| --- | --- |
| `GET /api/auth/session` | `{"authenticated", "username"}`，未登录时省略 `username` |
| `POST /api/auth/login` | `{"username", "password"}`，成功返回会话对象；超过频率返回 429 并带 `Retry-After` |
| `POST /api/auth/logout` | 结束会话，204 |
| `GET /api/config` | 配置视图（`config`、`token_configured`、`proxy_configured`、`restart_required`、`file`），`ETag` 为 revision |
| `PUT /api/config` | `{"config", "token_update", "proxy_update"}`，两个 update 省略时不变；返回新视图，后续步骤失败时另带 `warning` |
| `POST /api/config/reload` | 重新读取 YAML，返回同上 |
| `GET /api/runs` | 筛选参数 `state`、`source`、`trigger`、`from`（RFC 3339）、`range`（`1h`、`12h`、`24h`、`3d`、`7d`、`30d`、`yesterday`、`month`） |
| `GET /api/runs/{id}` | 任务详情 |
| `POST /api/sources/{id}/runs` | 手动创建任务，201 返回任务 |
| `POST /api/runs/{id}/{action}` | `action` 为 `pause`、`resume`、`cancel` |
| `GET /api/runs/{id}/logs` | `attempt`、`offset_bytes`；返回 `{"text", "next_offset_bytes", "cleared"}`；`download=true` 下载完整日志文本 |
| `POST /api/logs/clear` | 清理终态日志，返回 `{"cleared"}` |
| `GET /api/events` | SSE，`progress` 事件含 `runs`、`downloads`、`publications`、`schedules` |
| `GET /api/system/info`、`GET /api/system/checks` | 运行环境；工具、权限、reflink 与镜像盘检查 |
| `GET /api/analytics` | `type`、`days`（7、30、90）、`source` |
| `GET /api/notifications` | 渠道视图与时区，`ETag` 为 revision |
| `PATCH /api/notifications/{channel}` | JSON Merge Patch：`enabled`、`only_failure`、`endpoint`、`token`、`secret`、`chat_id`；省略的字段不变，`secret: null` 清除密钥，其他字段不接受 `null` |
| `POST /api/notifications/{channel}/test` | 用已保存的设置发送测试通知 |
| `GET /api/notifications/records` | 通知发送记录分页 |
| `PUT /api/notifications/timezone` | `{"timezone"}`，IANA 时区名 |

## 错误响应

```json
{"error": {"code": "source_busy", "message": "source already has an active, paused or interrupted run", "details": {}}}
```

- `code`：snake_case 错误码，是管理台分支和选择文案的唯一依据。
- `message`：英文开发者说明，不展示给用户。
- `details`：可选，结构按错误码定义。

| 状态码 | `code` | 含义 | `details` |
| --- | --- | --- | --- |
| 400 | `invalid_argument` | 请求体、查询参数、路径参数或配置字段无效 | `fields: [{path, reason}]`，见下节 |
| 400 | `config_malformed` | 配置文件不是一个有效 YAML 文档 | — |
| 400 | `unknown_action` | 任务操作不是 pause、resume、cancel | — |
| 400 | `unsupported_channel` | 通知渠道不受支持 | — |
| 401 | `unauthenticated` | 未登录或会话过期 | — |
| 401 | `invalid_credentials` | 登录用户名或密码错误 | — |
| 403 | `permission_denied` | 跨站写请求（`Sec-Fetch-Site` / `Origin` 不是本站） | — |
| 404 | `not_found` | 接口或记录不存在 | — |
| 405 | `method_not_allowed` | 方法不支持，带 `Allow` | — |
| 404 | `source_not_found`、`attempt_not_found` | 源或尝试编号不存在 | — |
| 409 | `restart_required` | 监听或数据目录已修改，需要重启 | — |
| 409 | `source_in_use` | 有保留任务的源不能删除、变更类型或 dest | — |
| 409 | `source_disabled`、`source_busy`、`source_removed`、`source_changed` | 源当前状态不允许该操作 | — |
| 409 | `run_not_active`、`run_not_pausable`、`run_not_resumable`、`executor_stopping`、`budget_exhausted` | 任务当前状态不允许该操作 | — |
| 409 | `log_cleanup_running` | 已有日志清理正在进行 | — |
| 409 | `notification_test_running` | 该渠道正在发送测试 | — |
| 409 | `idempotency_key_reused` | `Idempotency-Key` 已用于不同的请求 | — |
| 412 | `precondition_failed` | `If-Match` 缺失，或配置文件在载入后被修改 | — |
| 413 | `payload_too_large` | 请求体超过 1 MiB | — |
| 415 | `unsupported_media_type` | 请求体不是 `application/json` | — |
| 429 | `rate_limited` | 登录尝试过多，带 `Retry-After` | — |
| 500 | `internal`、`invalid_history`、`credentials_unreadable` | 服务端错误；未知错误的详情只写日志 | — |
| 503 | `notification_failed` | 测试通知发送失败 | `reason`（见通知记录）、可选 `status` |
| 503 | `unavailable` | 服务正在关闭，或查询暂时不可用 | — |
| 503 | `event_streams_full` | SSE 连接数已满 | — |
| 503 | `log_cleanup_failed` | 日志清理中途停止 | `cleared` |

日志清理失败时，`details.cleared` 给出已经清理的份数。配置写回成功但后续接入失败时，`PUT /api/config` 与 `POST /api/config/reload` 仍返回 200，`warning` 为 `{"code": "config_apply_incomplete", "message": "...", "details": {"detail": "..."}}`。

## 参数错误

`invalid_argument` 的 `details` 固定为 `{"fields": [{"path": "settings.rsync.io_timeout", "reason": "invalid_duration"}]}`：

- 配置字段的 `path` 是 YAML 点分路径，列表项使用下标（`sources.0.dest`）；请求参数使用字段名（`days`、`limit`、`endpoint`、`timezone`、`Idempotency-Key`）；整个请求体无效时为空字符串。
- `reason` 是 snake_case 原因码。配置字段的原因码由 `internal/config` 定义，请求解码、参数和通知字段另有 `unknown_field`、`invalid_type`、`invalid_json`、`not_nullable`、`invalid_integer`、`invalid_boolean`、`invalid_time`、`out_of_range`、`invalid_value`、`invalid_days`、`invalid_timezone`、`invalid_url`、`telegram_incomplete`、`wecom_webhook_required` 等。完整文案见 `web/src/app/config-errors.ts`，新增原因码时两端同时修改。

## 任务与尝试错误

`Run` 和 `Attempt` 的 `error_code` 为 snake_case 代码（如 `task_timeout`、`rsync_proxy`；执行被暂停、取消或服务中断时为 `paused`、`cancelled`、`interrupted`）；`error` 为英文说明，`error_details` 给出管理台组合中文所需的值：

| `error_code` | `error_details` |
| --- | --- |
| `github_api`、`github_proxy`、`download_failed`、`download_http`、`gpg_key_download` | `operation`（`github_api`、`asset_download` 等）；网络失败为 `route`（`direct`、`proxy`、`proxy_connect`）与 `reason`（`dns`、`tls_verify`、`timeout`、`refused`、`eof`、`proxy_auth` 等），HTTP 失败为 `http_status` |
| `github_rate_limit` | `reason`：`quota` 或 `secondary` |
| `rsync_proxy` | `reason`（如 `proxy_unreachable`、`proxy_auth_required`、`target_denied`），`detail` 为含代理地址与目标的英文诊断 |
| `invalid_source`、`invalid_asset`、`upstream_inconsistent` | `reason`；`upstream_inconsistent` 的 `invalid_metadata` 另带 `detail` |
| `source_exhausted` | `candidates`、`last_code` 与以 `last_` 为前缀的最后一次失败详情 |
| `upstream_error`、`partial_transfer`、`process_failed`、`local_transfer`、`reposync_failed`、`dependency_missing`、`output_failed`、`asset_verify`、`local_error` | `detail`：外部工具或系统的原始输出 |
| 代理失败且重试用尽 | 原代码，另加 `retries_exhausted: true` |

`error_details` 中的文本与 `error` 一样在写入前脱敏。

## 任务事件

`events` 的每项为 `{"at", "code", "message", "details"}`，`message` 为英文：

| `code` | `details` |
| --- | --- |
| `queued`、`requeued_after_restart`、`started`、`waiting_downloads` | — |
| `waiting_space` | 容量探测失败时 `probe_failed: true` |
| `action` | `action` |
| `state` | `state` |
| `reused_work` | `run` |
| `candidate` | `candidate` |
| `retry_scheduled` | `delay_ms`、`retry`、`allowed` |
| `run_error` | `error_code` 与该错误的 `error_details` |

## 通知发送记录

`GET /api/notifications/records` 的每项带 `error_code` 与可选 `error_details`。数据库的 error 列保存代码：`unconfirmed_after_restart`、`channel_disabled`、`failure_only`、`invalid_channel`、`credentials_unreadable`，以及发送失败的 `invalid_endpoint`、`network_failed`、`read_failed`、`response_too_large`、`invalid_response`、`robot_rejected`、`telegram_rejected`、`bark_rejected`、`interrupted`、`encode_failed`、`delivery_failed`；接收端 HTTP 失败保存为 `http_status:<状态码>`，接口返回 `error_code: "http_status"` 与 `error_details.status`。

## 环境检查

`GET /api/system/checks` 中：`reflink.status` 为 `supported`、`unsupported` 或 `error`，失败时 `detail` 为英文原因；`publication_issues` 每项带 `code`（`dest_not_directory`、`dest_outside_root`、`dest_orphan_generation`、`import_invalid`、`import_failed`）与英文 `error`；`storage.error` 为英文原因。

## 数值、空请求与幂等持久化

运行与分析的累计耗时为 `elapsed_ms`，运行预算为 `budget_ms`，可选进度 ETA 为 `eta_ms`，均为整数毫秒。旧 `_seconds` 字段不再接受或输出。所有时间字符串均为 UTC 固定三位毫秒。无请求字段的 POST 命令允许空 body 或 `{}`，拒绝未知字段、非对象与多段 JSON。日志的 `download` 参数仅接受 `true` / `false`；查询无效时返回 400。

运行和通知记录列表因界面需要跳页与总数，返回 `{items,total,limit,offset}`。`latest=true` 的 items 为每源最新状态，加上仍保留的任务，limit/total 均为返回的数量；它供概览使用，不替代历史分页。

带 Idempotency-Key 的任务创建在同一个 SQLite 写事务中记录新任务和第一次成功结果；后续任务状态变化不改变重放的首次响应。有效期 24 小时。同键的路径或 body 不同返回 409。临时 5xx / 429 不保存为最终结果。未上线期间没有旧路径、旧字段、旧错误码兼容层。
