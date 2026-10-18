import type { Source } from '../../app/types'

// Mirrors the backend rules in internal/config/config.go.
const idPattern = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$/
const repoPattern = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/
const reservedTopLevel = ['admin', 'api', 'favicon.ico']

export interface BatchRow {
  repository: string
  id: string
  name: string
  dest: string
  // Comma separated globs; empty means the shared patterns apply.
  patterns: string
  slug: string
  generatedDest: string
}

export interface ParsedRepositories {
  repositories: string[]
  invalid: string[]
}

// parseRepositories accepts owner/repo, github.com URLs and git@ remotes,
// separated by newlines, spaces or commas; duplicates are dropped
// case-insensitively because GitHub names are case-insensitive.
export function parseRepositories(text: string): ParsedRepositories {
  const repositories: string[] = []
  const invalid: string[] = []
  const seen = new Set<string>()
  for (const token of text.split(/[\s,]+/)) {
    if (!token) continue
    const repository = normalizeRepository(token)
    if (!repository) {
      if (!invalid.includes(token)) invalid.push(token)
      continue
    }
    const key = repository.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    repositories.push(repository)
  }
  return { repositories, invalid }
}

function normalizeRepository(token: string): string | null {
  const value = token
    .trim()
    .replace(/[?#].*$/, '')
    .replace(/^git@github\.com:/i, '')
    .replace(/^(?:https?:\/\/)?(?:www\.)?github\.com\//i, '')
  const [owner, repo] = value.split('/')
  // GitHub owners cannot contain dots, so gitlab.com/a/b is not owner/repo.
  if (!owner || !repo || owner.includes('.')) return null
  const repository = `${owner}/${repo.replace(/\.git$/i, '')}`
  return repoPattern.test(repository) ? repository : null
}

export function normalizePrefix(prefix: string) {
  return prefix.trim().split('/').filter(Boolean).join('/')
}

export function joinDest(prefix: string, slug: string) {
  const base = normalizePrefix(prefix)
  return base ? `${base}/${slug}` : slug
}

function slugify(value: string) {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[^a-z0-9]+/, '')
    .slice(0, 80)
}

function destsOverlap(a: string, b: string) {
  return a === b || a.startsWith(b + '/') || b.startsWith(a + '/')
}

// createRow picks the first slug whose id and dest are both free: the repo
// name, then owner-repo, then owner-repo-2 and so on.
export function createRow(
  repository: string,
  prefix: string,
  takenIds: Set<string>,
  takenDests: string[],
): BatchRow {
  const [owner, repo] = repository.split('/') as [string, string]
  const base = slugify(`${owner}-${repo}`) || 'release'
  const candidates = [slugify(repo), base]
  for (let suffix = 2; candidates.length < 100; suffix++) {
    candidates.push(`${base.slice(0, 80 - String(suffix).length - 1)}-${suffix}`)
  }
  const slug =
    candidates.find((candidate) => {
      if (!idPattern.test(candidate) || takenIds.has(candidate)) return false
      const dest = joinDest(prefix, candidate)
      return !takenDests.some((existing) => destsOverlap(existing, dest))
    }) ?? base
  const dest = joinDest(prefix, slug)
  return { repository, id: slug, name: repo, dest, patterns: '', slug, generatedDest: dest }
}

export function splitPatterns(value: string) {
  return value
    .split(',')
    .map((v) => v.trim())
    .filter(Boolean)
}

// staggerCrons spreads a single-number minute field across the rows so a
// batch of sources does not hit the GitHub API at the same moment. It
// returns null when the minute field is not a plain number.
export function staggerCrons(
  cron: string,
  count: number,
): { crons: string[]; step: number } | null {
  const fields = cron.trim().split(/\s+/)
  if (fields.length !== 5 || !/^\d{1,2}$/.test(fields[0]!) || Number(fields[0]) > 59) return null
  const minute = Number(fields[0])
  const step = Math.max(1, Math.min(10, Math.floor(60 / Math.max(count, 1))))
  const rest = fields.slice(1).join(' ')
  const crons = Array.from(
    { length: count },
    (_, index) => `${(minute + index * step) % 60} ${rest}`,
  )
  return { crons, step }
}

function destError(dest: string) {
  const segments = dest.split('/')
  if (!dest || dest.startsWith('/') || segments.some((segment) => !segment))
    return '须为规范相对目录'
  if (/[\\\0\r\n]/.test(dest)) return '包含非法字符'
  if (reservedTopLevel.includes(segments[0]!)) return '包含保留路径'
  if (segments.some((segment) => segment.startsWith('.'))) return '目录段不能以 . 开头'
  return ''
}

export interface RowErrors {
  id?: string
  name?: string
  dest?: string
}

// validateRows checks each row against the configured sources and the
// other rows, so conflicts show before the backend rejects the save.
export function validateRows(rows: BatchRow[], existing: Source[]): RowErrors[] {
  return rows.map((row, index) => {
    const errors: RowErrors = {}
    const others = rows.filter((_, other) => other !== index)
    if (!idPattern.test(row.id)) errors.id = '字母或数字开头，仅含字母、数字、. _ -'
    else if (existing.some((source) => source.id === row.id)) errors.id = '与已有同步源重复'
    else if (others.some((other) => other.id === row.id)) errors.id = '与其他行重复'
    if (!row.name.trim()) errors.name = '不能为空'
    const invalid = destError(row.dest)
    if (invalid) errors.dest = invalid
    else if (existing.some((source) => destsOverlap(source.dest, row.dest)))
      errors.dest = '与已有同步源重复或嵌套'
    else if (others.some((other) => destsOverlap(other.dest, row.dest)))
      errors.dest = '与其他行重复或嵌套'
    return errors
  })
}
