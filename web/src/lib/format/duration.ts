/** A duration in seconds as 45s, 3m20s or 2h15m; — when missing. */
export function formatDuration(seconds?: number | null): string {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0) return '—'
  const whole = Math.floor(seconds)
  if (whole < 60) return `${String(whole)}s`
  if (whole < 3600) return `${String(Math.floor(whole / 60))}m${String(whole % 60)}s`
  return `${String(Math.floor(whole / 3600))}h${String(Math.floor((whole % 3600) / 60))}m`
}

/** Compact duration for chart axes, dropping zero-valued trailing units. */
export function formatDurationTick(seconds: number): string {
  if (seconds > 0 && seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds > 0 && seconds < 3600 && seconds % 60 === 0) return `${seconds / 60}m`
  return formatDuration(seconds)
}

/** Formats optional integer millisecond values received from the API. */
export function formatDurationMillis(milliseconds?: number | null): string {
  return formatDuration(typeof milliseconds === 'number' ? milliseconds / 1000 : undefined)
}
