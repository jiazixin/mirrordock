export type SourceType = 'rsync' | 'reposync' | 'github_release'
export type RsyncProxyMode = 'global' | 'direct' | 'custom'
export type Source = {
  id: string
  name: string
  type: SourceType
  enabled: boolean
  dest: string
  exclude: string[]
  upstreams: Record<string, string>
  rsync: {
    preserve_links?: boolean
    preserve_times?: boolean
    preserve_permissions?: boolean
    preserve_hardlinks?: boolean
    delete?: boolean
    uid?: number
    gid?: number
    proxy_mode?: '' | RsyncProxyMode
    proxy?: string
  }
  reposync: {
    repo_id: string
    releasever: string
    basearch: string
    architectures: string[]
    download_metadata?: boolean
    newest_only: boolean
    delete?: boolean
    gpgcheck?: boolean
    gpg_keys: string[]
    module_hotfixes: boolean
  }
  github_release: { repository: string; release_count: number; asset_patterns: string[] }
  schedule: { enabled: boolean; cron: string; overlap: string }
}
export type Configuration = {
  listen: string
  port: number
  path: string
  mirror_root: string
  auth: { username: string }
  log: { level: 'debug' | 'info' | 'warn' | 'error'; path: string; format: 'json' | 'text' }
  tls: { cert: string; key: string }
  settings: {
    retry_count: number
    task_timeout: string
    concurrency: { rsync: number; reposync: number; github_release: number }
    rsync: { io_timeout: string; connect_timeout: string; limit_mb: number; proxy: string }
    github: {
      release_count: number
      include_prereleases: boolean
      prune_old_releases: boolean
      token: string
      proxy: string
    }
  }
  sources: Source[]
}
export type ConfigView = {
  config: Configuration
  revision: string // from the ETag; not part of the response body
  token_configured: boolean
  proxy_configured: boolean
  restart_required: boolean
  file: string
  warning?: { code: string; message: string; details?: Record<string, unknown> }
}
export type Attempt = {
  number: number
  candidate: string
  started_at: string
  ended_at?: string
  error_code: string
  error: string
  error_details?: Record<string, unknown>
  exit_code: number
  log_cleared: boolean
  received_bytes?: number
}
export type Run = {
  id: number
  source_id: string
  name: string
  type: SourceType
  dest: string
  state: string
  trigger: string
  created_at: string
  started_at?: string
  ended_at?: string
  elapsed_ms: number
  budget_ms: number
  candidate_index: number
  error_code: string
  error: string
  error_details?: Record<string, unknown>
  progress: {
    phase: string
    received_bytes?: number
    received_incomplete?: boolean
    transferred_bytes: number
    total_bytes: number
    percent?: number
    speed_bytes_per_second: number
    eta_ms?: number
    files: number
    success: number
    failed: number
    skipped: number
  }
  attempts: Attempt[]
  events: { at: string; code: string; message: string; details?: Record<string, unknown> }[]
  snapshot: { source: Source; settings: Configuration['settings'] }
}
export type Publication = {
  source_id: string
  dest: string
  current: { id: string; directory: string; published_at: string }
  previous: unknown[]
}
export type SystemInfo = {
  timezone: string
  utc_offset_seconds: number
  version: string
  go: string
  os: string
  arch: string
  cpus: number
  tls: boolean
  deployment: {
    listen: string
    data: string
    database: string
    mirror_root: string
    log: Configuration['log']
    tls: Configuration['tls']
  }
  publications: Publication[]
  downloads: {
    active: number
    requests: number
    sent_bytes: number
    capacity: number
    large_capacity: number
    metadata_capacity: number
  }
  schedules: Record<string, string>
}
export type SystemChecks = {
  reflink: { status: 'supported' | 'unsupported' | 'error'; detail: string }
  storage: {
    total_bytes: number
    available_bytes: number
    reserve_bytes: number
    required_bytes: number
    workers: number
    previous: number
    work_directories: number
    error?: string
  }
  rsync: { available: boolean; version: string }
  dnf: { available: boolean; version: string }
  rpmkeys: { available: boolean; version: string }
  config_writable: boolean
  mirror_writable: boolean
  github_token_configured: boolean
  github_proxy_configured: boolean
  publication_issues?: { source_id: string; code: string; error: string }[]
}
export type AnalyticsStats = {
  runs: number
  succeeded: number
  failed: number
  cancelled: number
  active: number
  duration_samples: number
  elapsed_ms: number
  transferred_bytes: number
  files: number
  completed: number
  reused: number
  attempts: number
  failed_attempts: number
  retried_runs: number
  fallback_succeeded: number
}
export type AnalyticsDay = {
  date: string
} & AnalyticsStats
export type AnalyticsSource = {
  id: string
  name: string
} & AnalyticsStats
export type Analytics = {
  type: SourceType
  from: string
  until: string
  timezone: string
  summary: AnalyticsStats
  days: AnalyticsDay[]
  sources: AnalyticsSource[]
  errors: { code: string; count: number }[]
}
