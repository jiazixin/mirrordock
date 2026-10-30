// Console text for codes reported by the server. The server sends stable codes
// with English developer text; the console shows the text mapped here and
// never the server's English, except raw tool output in details.detail. Records
// use the current code catalog. See
// docs/API.md.

import { formatDurationMillis } from '../lib/format/duration'
import { configFieldText, isErrorField } from './config-errors'

export type ErrorBody = {
  code: string
  message?: string
  details?: Record<string, unknown>
}

type Details = Record<string, unknown> | undefined

function text(value: unknown): string {
  return typeof value === 'string' || typeof value === 'number' ? String(value) : ''
}

// withDetail appends raw tool or upstream output to a console text.
function withDetail(summary: string, details: Details): string {
  const detail = text(details?.detail).trim()
  return detail ? `${summary}：${detail}` : summary
}

const apiMessages: Record<string, string | ((details: Details) => string)> = {
  not_found: '请求的记录或接口不存在',
  unavailable: '服务暂时不可用，请稍后重试',
  permission_denied: '请求未通过同源校验，请从当前控制台提交',
  unauthenticated: '请先登录',
  invalid_credentials: '用户名或密码错误',
  rate_limited: '尝试过多，一分钟后重试',
  payload_too_large: '请求内容超过 1 MiB 上限',
  unsupported_media_type: '请求格式无效，请刷新页面后重试',
  method_not_allowed: '接口不支持该操作，请刷新页面后重试',
  internal: '服务端内部错误，请稍后重试',
  event_streams_full: '事件连接数量已满',
  precondition_failed: '配置已被修改，请重新加载后再保存',
  idempotency_key_reused: '重复提交的请求与首次不同，请刷新后重试',
  config_malformed: '配置文件格式无效',
  restart_required: '监听或数据目录配置已修改，请先重启',
  source_in_use: '活动、暂停或中断任务的源不能删除或变更类型、dest，请先取消任务',
  source_not_found: '源不存在',
  source_disabled: '源已禁用，启用后再操作',
  source_busy: '该源已有活动、暂停或中断任务',
  source_removed: '源已从配置删除，不能继续；请取消任务',
  source_changed: '源类型或 dest 已变更，不能继续；请取消任务',
  run_not_active: '仅可操作活动或中断任务',
  run_not_pausable: '当前任务不可暂停',
  executor_stopping: '执行器正在停止，请等待退出后继续',
  run_not_resumable: '当前任务不可继续',
  budget_exhausted: '累计预算已耗尽',
  unknown_action: '未知任务操作',
  attempt_not_found: '尝试编号不存在',
  log_cleanup_running: '已有日志清理正在进行',
  log_cleanup_failed: (details) => `日志清理已中断，已清理 ${text(details?.cleared) || 0} 份`,
  invalid_history: '历史任务编号无效，日志清理已停止',
  unsupported_channel: '不支持该通知渠道',
  notification_test_running: '该渠道正在发送测试，请稍后',
  credentials_unreadable: '通知凭据无法解密，请检查密钥备份',
  notification_failed: (details) => deliveryText(text(details?.reason), details),
}

/** Returns the console text for a failed API response. */
export function apiErrorText(body: ErrorBody | undefined, status: number): string {
  if (!body) return statusText(status)
  if (body.code === 'invalid_argument') {
    const fields = body.details?.fields
    const field: unknown = Array.isArray(fields) ? fields[0] : undefined
    return isErrorField(field) ? configFieldText(field) : '请求参数无效'
  }
  const message = apiMessages[body.code]
  if (typeof message === 'function') return message(body.details)
  return message ?? statusText(status)
}

const networkReasons: Record<string, string> = {
  network: '网络连接失败，请检查网络',
  dns: 'DNS 解析失败，请检查目标或代理的域名解析',
  tls_verify: 'TLS 证书校验失败，请检查目标或 HTTPS 代理的证书',
  tls_untrusted: 'TLS 证书不受信任，请检查目标或 HTTPS 代理的证书',
  timeout: '网络等待超时，请检查目标或代理的连接',
  refused: '连接被拒绝，请检查目标或代理的监听地址与端口',
  eof: '连接被提前关闭（EOF）',
  proxy_auth: '代理要求认证（HTTP 407），请检查代理用户名和密码',
}

const operations: Record<string, string> = {
  github_api: 'GitHub API 查询',
  asset_download: '资产下载',
  asset_redownload: '资产重新下载',
  asset_stream: '资产响应流中断，已保存暂存文件；',
  gpg_key_download: 'GPG 公钥下载',
  gpg_key_read: 'GPG 公钥读取',
}

const githubStatus: Record<number, string> = {
  401: 'Token 无效或已过期，请重新配置',
  403: '访问被拒绝，请检查 Token 权限或 API 限流',
  404: '仓库或附件不存在，或 Token 无权访问',
  407: '代理要求认证，请检查代理用户名和密码',
}

const routes: Record<string, string> = { proxy_connect: '未能建立代理连接', proxy: '经代理失败' }

const gpgReasons: Record<string, string> = {
  invalid_url: 'GPG 公钥地址无效',
  invalid_size: 'GPG 公钥必须非空且不超过 1 MiB',
}

// networkText describes a network failure from its operation, route and reason.
function networkText(details: Details): string {
  const operation = operations[text(details?.operation)] ?? '网络请求'
  const status = Number(details?.http_status)
  if (status) {
    const reason =
      details?.operation === 'github_api'
        ? (githubStatus[status] ?? '请求未成功，请检查 GitHub 服务状态')
        : '请求未成功'
    return `${operation} HTTP ${status}：${reason}`
  }
  const route = text(details?.route)
  const via = routes[route] ?? '直连失败'
  let reason = networkReasons[text(details?.reason)] ?? networkReasons.network ?? ''
  if (route !== 'direct' && route) {
    reason +=
      details?.proxy_scheme === 'https'
        ? '；https:// 要求代理入口本身支持 TLS，HTTP/mixed 入口应使用 http://'
        : '；请确认代理协议、端口及 CONNECT/SOCKS 转发是否可用'
  }
  return `${operation}${via}：${reason}`
}

const proxyReasons: Record<string, string> = {
  invalid_proxy: '代理地址无效',
  executable_missing: '无法定位 MirrorDock 可执行文件，不能经代理连接',
  invalid_target: '上游地址无效',
  invalid_timeout: '连接超时无效',
  target_host_too_long: '上游主机名过长',
  proxy_unreachable: '无法连接代理',
  proxy_tls_timeout: '代理 TLS 握手超时',
  proxy_tls_failed: '代理 TLS 握手失败；https:// 要求代理入口本身支持 TLS',
  proxy_handshake_failed: '与代理握手失败',
  proxy_handshake_timeout: '与代理握手超时',
  proxy_bad_response: '代理没有返回有效的 HTTP 响应，请确认代理协议与端口',
  proxy_auth_required: '代理要求认证（HTTP 407），请检查代理用户名和密码',
  target_denied: '代理拒绝连接上游，请确认代理允许 CONNECT 到该端口',
  target_failed: '代理无法连接上游',
  connect_unsupported: '代理不支持 CONNECT 到上游',
  not_socks5: '代理不是 SOCKS5 代理，请确认代理协议与端口',
  socks_auth_required: 'SOCKS5 代理要求用户名和密码',
  socks_auth_unsupported: 'SOCKS5 代理不接受可用的认证方式',
  socks_bad_response: 'SOCKS5 代理返回了无效响应',
  socks_auth_failed: 'SOCKS5 代理认证失败，请检查代理用户名和密码',
}

const runMessages: Record<string, string | ((details: Details) => string)> = {
  service_interrupted: '服务中断，可使用剩余预算继续',
  task_timeout: '累计执行时间达到任务上限',
  persistence_failed: '任务状态保存失败，任务目录仍保留；修复存储后继续或取消',
  disk_space: '镜像盘可用空间低于安全余量；任务已停止，上一发布代保持可用',
  source_removed: '源已从配置删除，任务已自动取消',
  source_changed: '源类型或 dest 已变更，任务已自动取消',
  source_disabled: '源已禁用；启用后可继续，或取消任务',
  paused: '执行已停止：已暂停',
  cancelled: '执行已停止：已取消',
  interrupted: '执行已停止：服务中断',
  no_candidates: '没有可用来源',
  source_exhausted: (details) => {
    const last: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(details ?? {}))
      if (key.startsWith('last_') && key !== 'last_code') last[key.slice(5)] = value
    const reason = runErrorText({
      error_code: text(details?.last_code),
      error: '',
      error_details: last,
    })
    return `所有 ${text(details?.candidates)} 个来源均失败，重试次数已用尽；最近原因：${reason}`
  },
  upstream_inconsistent: (details) =>
    withDetail(
      {
        empty_directory: '同步目录为空，无法发布',
        missing_repodata: '仓库缺少 repodata 元数据',
      }[text(details?.reason)] ?? '仓库元数据不一致',
      details?.reason === 'invalid_metadata' ? details : undefined,
    ),
  upstream_error: (details) => withDetail('上游返回错误', details),
  partial_transfer: (details) => withDetail('部分文件传输失败', details),
  process_failed: (details) => withDetail('执行器异常退出', details),
  local_transfer: (details) => withDetail('本地写入失败', details),
  reposync_failed: (details) => withDetail('reposync 执行失败', details),
  dependency_missing: (details) => withDetail('缺少依赖程序', details),
  output_failed: (details) => withDetail('执行器输出处理失败', details),
  local_error: (details) => withDetail('本地错误', details),
  invalid_type: (details) => `不支持的源类型 ${text(details?.type)}`,
  gpg_key_download: (details) => gpgReasons[text(details?.reason)] ?? networkText(details),
  rsync_proxy: (details) =>
    withDetail(`经代理连接失败：${proxyReasons[text(details?.reason)] ?? '代理连接异常'}`, details),
  invalid_source: (details) =>
    details?.reason === 'invalid_port'
      ? 'rsync 来源端口必须在 1–65535 之间'
      : '经代理连接需要 rsync:// 来源地址',
  no_assets: '所选 Release 没有匹配的附件，不发布空镜像',
  release_limit: 'Release 列表超过查询上限，请缩小仓库范围',
  invalid_tag: 'Release tag 无法转换成安全目录',
  asset_limit: '资产列表超过查询上限',
  invalid_asset: (details) =>
    ({
      unsafe_name: '资产名称不安全或与内部保留名称冲突',
      symlink_directory: 'Release 目录不能是符号链接',
      not_directory: 'Release 目录必须为真实目录',
      not_regular_file: '资产路径必须是普通文件',
    })[text(details?.reason)] ?? '资产无效',
  asset_verify: (details) => withDetail('资产校验失败', details),
  asset_size: '下载超过声明大小',
  download_idle: '资产正文长时间无数据，已保存暂存文件',
  download_http: (details) => networkText(details),
  download_failed: (details) =>
    details?.operation === 'asset_stream' && details.reason === 'eof' && !details.route
      ? '资产响应流提前结束，已保存暂存文件'
      : networkText(details),
  github_api: (details) => networkText(details),
  github_proxy: (details) => networkText(details),
  github_rate_limit: (details) =>
    details?.reason === 'secondary'
      ? 'GitHub API 触发临时限流，请稍后重试'
      : 'GitHub API 配额已用尽，等待限流重置后重试',
}

type RunError = {
  error_code: string
  error: string
  error_details?: Record<string, unknown>
}

/** Returns the console text of a run or attempt error. */
export function runErrorText(item: RunError): string {
  if (!item.error_code) return ''
  const message = runMessages[item.error_code]
  let summary = typeof message === 'function' ? message(item.error_details) : message
  if (summary === undefined) summary = withDetail(`错误 ${item.error_code}`, item.error_details)
  return item.error_details?.retries_exhausted
    ? `代理不可用，重试次数已用尽；最近原因：${summary}`
    : summary
}

type RunEvent = {
  code: string
  message: string
  details?: Record<string, unknown>
}

const actions: Record<string, string> = { pause: '暂停', resume: '继续', cancel: '取消' }
const states: Record<string, string> = {
  succeeded: '成功',
  failed: '失败',
  timed_out: '超时',
  cancelled: '已取消',
  interrupted: '中断',
  paused: '已暂停',
  queued: '排队中',
}

/** Returns the console text of a run history event. */
export function eventText(event: RunEvent): string {
  const details = event.details
  switch (event.code) {
    case 'queued':
      return '任务已加入队列'
    case 'requeued_after_restart':
      return '服务恢复，自动排队继续；沿用累计预算及重试次数'
    case 'run_error':
      return runErrorText({
        error_code: text(details?.error_code),
        error: event.message,
        error_details: details,
      })
    case 'action':
      return `操作：${actions[text(details?.action)] ?? '未知操作'}`
    case 'waiting_downloads':
      return '公开下载繁忙，保持排队；负载降低后自动执行'
    case 'waiting_space':
      return details?.probe_failed
        ? '镜像盘容量探测失败，保持排队；恢复后自动继续'
        : '镜像盘可用空间不足，保持排队；释放空间后自动继续'
    case 'started':
      return '开始执行'
    case 'state':
      return `任务状态：${states[text(details?.state)] ?? '未知状态'}`
    case 'reused_work':
      return `复用任务 #${text(details?.run)} 保留的未完成同步目录`
    case 'candidate':
      return `尝试来源：${text(details?.candidate)}`
    case 'retry_scheduled':
      return `网络或上游临时失败，${formatDurationMillis(typeof details?.delay_ms === 'number' ? details.delay_ms : undefined)} 后自动重试（${text(details?.retry)}/${text(details?.allowed)}）`
    default:
      return `任务事件 ${event.code}`
  }
}

const deliveryReasons: Record<string, string> = {
  invalid_endpoint: '推送地址无效',
  encode_failed: '通知编码失败',
  interrupted: '发送已中断',
  network_failed: '通知网络请求失败或超时，请检查网络和配置',
  read_failed: '通知响应读取失败',
  response_too_large: '通知响应超过 64 KiB',
  invalid_response: '通知接收端响应格式无效',
  robot_rejected: '机器人拒绝通知，请检查凭据及机器人安全规则',
  telegram_rejected: 'Telegram 拒绝通知，请检查 Token、Chat ID 和机器人权限',
  bark_rejected: 'Bark 拒绝通知，请检查设备 Key 和服务配置',
  unconfirmed_after_restart: '发送状态未确认；服务重启恢复，可能重复送达',
  channel_disabled: '渠道已关闭',
  failure_only: '不符合仅失败通知条件',
  invalid_channel: '通知配置无效，请重新保存',
  credentials_unreadable: '通知凭据无法解密，请检查密钥备份',
  delivery_failed: '通知发送失败',
}

function deliveryText(reason: string, details: Details): string {
  if (reason === 'http_status') return `通知接收端返回 HTTP ${text(details?.status)}`
  return deliveryReasons[reason] ?? '通知发送失败'
}

type DeliveryRecord = {
  error_code?: string
  error_details?: Record<string, unknown>
}

/** Returns the console text of a notification delivery record's error. */
export function recordErrorText(record: DeliveryRecord): string {
  return record.error_code ? deliveryText(record.error_code, record.error_details) : ''
}

const issueMessages: Record<string, string> = {
  dest_not_directory: 'dest 被非目录占用',
  dest_outside_root: 'dest 解析后越出镜像根，拒绝导入',
  dest_orphan_generation: 'dest 指向内部发布代但没有对应数据库记录，请恢复数据库备份',
  import_invalid: '已有目录不是完整的仓库，无法导入',
  import_failed: '已有目录导入失败',
}

/** Returns the console text of a publication issue. */
export function issueText(issue: { code: string; error: string }): string {
  const summary = issueMessages[issue.code] ?? '发布目录接入失败'
  return issue.code === 'import_invalid' || issue.code === 'import_failed'
    ? `${summary}：${issue.error}`
    : summary
}

/** Returns the console text of the reflink probe result. */
export function reflinkText(reflink: { status: string; detail: string } | undefined): string {
  if (!reflink) return '尚未取得镜像目录的实测结果'
  if (reflink.status === 'supported')
    return '镜像目录实测支持写时复制；未变化文件共享数据块，克隆失败将停止任务'
  return withDetail('镜像目录克隆未成功', { detail: reflink.detail })
}

/** Returns the console text of a configuration apply warning. */
export function warningText(warning: ErrorBody): string {
  if (warning.code === 'config_apply_incomplete')
    return withDetail(
      '配置已写回，但发布目录接入或计划更新失败，请修复后重新载入配置',
      warning.details,
    )
  return apiErrorText(warning, 0)
}

function statusText(status: number): string {
  const messages: Record<number, string> = {
    400: '请求参数无效',
    401: '请先登录',
    403: '没有执行此操作的权限',
    404: '请求的记录或接口不存在',
    405: '接口不支持该操作',
    409: '当前状态不允许此操作',
    412: '配置已被修改，请重新加载后再保存',
    413: '请求内容过大',
    415: '请求格式不受支持',
    429: '请求过于频繁，请稍后重试',
    500: '服务端内部错误，请稍后重试',
    503: '服务暂时不可用，请稍后重试',
  }
  return messages[status] ?? `请求失败（HTTP ${status}）`
}
