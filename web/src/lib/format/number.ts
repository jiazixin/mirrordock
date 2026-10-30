/** A count or measure with Chinese digit grouping and at most one decimal. */
export function formatNumber(value?: number): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 1 })
}

/** part / whole as a percentage number with one decimal, without the sign. */
export function formatRate(part: number, whole: number): string {
  if (!Number.isFinite(part) || !Number.isFinite(whole) || whole <= 0) return '—'
  return ((100 * part) / whole).toFixed(1)
}

/** A percentage value (0–100) with the given decimals, such as 42.5%. */
export function formatPercent(percent: number | undefined, digits = 1): string {
  if (typeof percent !== 'number' || !Number.isFinite(percent)) return '—'
  return `${percent.toFixed(digits)}%`
}
