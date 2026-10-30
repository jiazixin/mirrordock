// Console text for invalid request fields. The server reports them as
// invalid_argument with the field path and a reason code; configuration fields
// use their dotted YAML path.

export type ErrorField = {
  path: string
  reason: string
}

export function isErrorField(value: unknown): value is ErrorField {
  return (
    typeof value === 'object' &&
    value !== null &&
    'path' in value &&
    typeof value.path === 'string' &&
    'reason' in value &&
    typeof value.reason === 'string'
  )
}

const reasons: Record<string, string> = {
  required: '不能为空',
  retry_count_range: '必须为 0–10 之间的整数',
  concurrency_range: '每种类型必须为 1–16 之间的整数',
  invalid_log_level: '只支持 debug、info、warn、error',
  invalid_log_format: '只支持 json、text',
  untrimmed_text: '不能包含首尾空白或控制字符',
  invalid_username: '必须为 1–128 字节，不能包含首尾空白或控制字符',
  invalid_password: '必须非空，最多 72 字节',
  invalid_listen: '必须是 IP 地址或 localhost',
  port_range: '端口必须在 1–65535 之间',
  invalid_duration: '必须为正时间长度，例如 30s 或 8h',
  negative_number: '必须为非负数',
  release_count_range: '必须在 1–100 之间',
  source_release_count_range: '必须在 0–100 之间',
  invalid_token: '不能包含空白或控制字符，请只填写 Token 本身',
  path_unresolvable: '路径无法解析或无法访问',
  mirror_root_not_isolated: '镜像根必须与数据库、日志和配置目录隔离，且不能是文件系统根',
  inside_mirror_root: '不能位于镜像根中',
  inside_task_logs: '不能使用任务日志子目录；可使用 <path>/logs/mirrordock.log',
  not_regular_file: '必须是普通文件',
  protected_file: '不能覆盖配置、数据库、实例锁、通知密钥或 TLS 文件',
  tls_pair_incomplete: 'TLS 证书与私钥必须同时填写',
  tls_load_failed: 'TLS 证书或私钥加载失败',
  invalid_identifier: '必须为 1–80 个字母、数字、点、下划线或连字符，并以字母或数字开头',
  duplicate_id: '源 id 重复',
  dest_overlap: '目标目录不能重复或互相嵌套',
  dest_outside_mirror_root: '解析符号链接后超出镜像根',
  invalid_dest: '必须是规范的相对目录',
  reserved_dest: '不能以保留路径开头（admin、api、favicon.ico）',
  hidden_dest_segment: '不能包含以 . 开头的路径段',
  unknown_source_field: '不支持该字段，仅接受 src1、src2…',
  source_number_range: 'src 编号超出整数范围',
  control_characters: '不能包含控制字符',
  invalid_http_url: '需要合法的 HTTP/HTTPS 地址',
  url_has_credentials: '不能包含用户信息、查询参数或片段；不支持 URL 鉴权',
  invalid_source_type: '只支持 rsync、reposync、github_release',
  invalid_rsync_url: '必须是 rsync:// 地址，不能包含用户信息、查询参数或片段',
  missing_rsync_module: '必须包含 rsync 模块，模块内目录须以 / 结尾',
  id_range: '必须在 0–4294967294 之间',
  proxy_not_custom: '仅在 proxy_mode 为 custom 时填写',
  proxy_required: 'proxy_mode 为 custom 时必须填写',
  invalid_proxy_mode: '只支持 global、direct、custom',
  reposync_single_source: 'reposync 只接受一个来源地址 src1',
  newest_only_with_metadata: '下载完整 metadata 时不能启用 newest_only',
  gpg_keys_required: '启用 GPG 检查时必须填写 gpg_keys',
  invalid_repository: '必须为 <owner>/<repository>',
  invalid_glob: '不是有效的 glob 模式',
  invalid_overlap: '只支持 skip 或留空',
  invalid_cron_fields: '必须为五个字段，并使用程序启动时区',
  invalid_cron: '不是有效的 cron 表达式',
  invalid_proxy_url: '必须为合法的代理地址，不能包含路径、查询或片段',
  invalid_proxy_scheme: '只支持 http、https、socks5、socks5h',
  invalid_proxy_userinfo: '代理认证信息无效',
  unsupported_channel: '不支持该通知渠道，可用 wecom、dingtalk、telegram、bark、webhook',
  plaintext_credentials: '凭据必须通过通知设置加密保存',
  channel_fields_mismatch: '字段与通知渠道不匹配',
  unknown_field: '不支持该字段',
  invalid_type: '类型不正确',
  invalid_json: '必须是一个有效的 JSON 对象',
  invalid_days: '时间范围须为 7、30 或 90 天',
  invalid_value: '取值无效',
  not_nullable: '不能清空',
  invalid_integer: '必须是整数',
  invalid_boolean: '必须是 true',
  invalid_time: '必须是 RFC 3339 时间',
  out_of_range: '超出允许范围',
  invalid_timezone: 'IANA 时区无效',
  invalid_url: '必须是有效 HTTP/HTTPS URL，不能含用户名密码或 fragment',
  invalid_port: '端口无效',
  invalid_text: '格式或长度无效',
  invalid_format: '格式无效',
  invalid_query: '参数无效',
  invalid_path: '路径前缀无效',
  telegram_incomplete: '请填写 Bot Token 和 Chat ID',
  wecom_webhook_required: '请填写企业微信群机器人完整官方 Webhook（含 key）',
  dingtalk_webhook_required: '请填写钉钉机器人完整官方 Webhook（含 access_token，不含临时签名）',
  bark_key_required: 'Bark 地址必须以设备 Key 结尾，请删除示例标题和正文',
  fields_not_for_channel: '通知字段与渠道不匹配',
  clear_with_new_secret: '清除密钥时不能同时填写新密钥',
}

// Labels of request fields outside the configuration file.
const labels: Record<string, string> = {
  endpoint: '推送地址',
  token: 'Bot Token',
  secret: '密钥',
  chat_id: 'Chat ID',
  timezone: '时区',
  days: '时间范围',
  range: '时间范围',
  type: '分析类型',
}

// fieldLabel turns "sources.0.dest" into "第 1 个源的 dest".
function fieldLabel(path: string): string {
  const source = /^sources\.(\d+)(?:\.(.+))?$/.exec(path)
  if (!source) return labels[path] ?? path
  const label = `第 ${Number(source[1]) + 1} 个源`
  return source[2] ? `${label}的 ${source[2]}` : label
}

// configFieldText returns the console text for an invalid field.
export function configFieldText(field: ErrorField): string {
  const reason = reasons[field.reason] ?? '无效'
  if (!field.path)
    return field.reason === 'invalid_json' ? '请求体必须是一个有效的 JSON 对象' : reason
  return `${fieldLabel(field.path)}：${reason}`
}
