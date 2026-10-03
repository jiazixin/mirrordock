export type SourceType = 'rsync' | 'reposync' | 'github_release'
export interface Source {
  id: string
  name: string
  type: SourceType
  enabled: boolean
  dest: string
  exclude: string[]
  upstreams: Record<string, string>
  rsync: {
    preserve_links: boolean | null
    preserve_times: boolean | null
    preserve_permissions: boolean | null
    preserve_hardlinks: boolean | null
    delete: boolean | null
    uid: number | null
    gid: number | null
  }
  reposync: {
    repo_id: string
    releasever: string
    basearch: string
    architectures: string[]
    download_metadata: boolean | null
    newest_only: boolean
    delete: boolean | null
    gpgcheck: boolean | null
    gpg_keys: string[]
    module_hotfixes: boolean
  }
  github_release: { repository: string; release_count: number; asset_patterns: string[] }
  schedule: { enabled: boolean; cron: string; overlap: string }
}
export interface Configuration {
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
    rsync: { io_timeout: string; connect_timeout: string; limit_mb: number }
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
export interface ConfigView {
  config: Configuration
  revision: string
  token_configured: boolean
  proxy_configured: boolean
  restart_required: boolean
  file: string
}
export interface Attempt {
  number: number
  candidate: string
  started_at: string
  ended_at?: string
  error_code: string
  error: string
  exit_code: number
  log_cleared: boolean
  received_bytes?: number | null
}
export interface Run {
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
  elapsed_seconds: number
  budget_seconds: number
  candidate_index: number
  error_code: string
  error: string
  progress: {
    phase: string
    received_bytes?: number | null
    received_incomplete?: boolean
    bytes: number
    total: number
    percent: number | null
    speed: number
    eta_seconds: number | null
    files: number
    success: number
    failed: number
    skipped: number
  }
  attempts: Attempt[]
  events: { at: string; message: string }[]
  snapshot: { source: Source; settings: Configuration['settings'] }
}
export interface Publication {
  source_id: string
  dest: string
  current: { id: string; directory: string; published_at: string }
  previous: unknown[]
}
export interface SystemInfo {
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
  downloads: { active: number; requests: number; bytes: number; capacity: number }
  schedules: Record<string, string>
}
export interface SystemChecks {
  reflink: { status: 'supported' | 'unsupported' | 'error'; detail: string }
  storage: {
    total: number
    available: number
    reserve: number
    required: number
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
}
export interface AnalyticsStats {
  runs: number
  succeeded: number
  failed: number
  cancelled: number
  active: number
  duration_samples: number
  elapsed_seconds: number
  bytes: number
  files: number
  completed: number
  reused: number
  attempts: number
  failed_attempts: number
  retried_runs: number
  fallback_succeeded: number
}
export interface AnalyticsDay extends AnalyticsStats {
  date: string
}
export interface AnalyticsSource extends AnalyticsStats {
  id: string
  name: string
}
export interface Analytics {
  type: SourceType
  from: string
  until: string
  timezone: string
  summary: AnalyticsStats
  days: AnalyticsDay[]
  sources: AnalyticsSource[]
  errors: { code: string; count: number }[]
}
