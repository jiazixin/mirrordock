/** Server startup time zone and fallback offset for every timestamp display. */
export type ServerZone = {
  timezone?: string | undefined // IANA name; "Local" or missing uses the offset
  utcOffsetSeconds?: number | undefined // the server's offset at startup
}

/**
 * A timestamp as YYYY/MM/DD HH:mm:ss in the server's time zone; — for an empty
 * or zero time.
 */
export function formatServerTime(value: string | undefined, zone: ServerZone): string {
  if (!value || value.startsWith('0001')) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  try {
    if (zone.timezone && zone.timezone !== 'Local')
      return new Intl.DateTimeFormat('zh-CN', {
        timeZone: zone.timezone,
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(date)
  } catch {
    /* use startup offset */
  }
  const shifted = new Date(date.getTime() + (zone.utcOffsetSeconds ?? 0) * 1000)
  return shifted.toISOString().slice(0, 19).replace('T', ' ')
}

/** UTC-shifted ISO time used by the server-offset timeline. */
export function shiftedTime(milliseconds: number, offsetMilliseconds: number): string {
  return new Date(milliseconds + offsetMilliseconds).toISOString()
}
/** Timeline clock label using the startup UTC offset. */
export function formatClock(milliseconds: number, offsetMilliseconds: number): string {
  return shiftedTime(milliseconds, offsetMilliseconds).slice(11, 16)
}
/** Timeline calendar label using the startup UTC offset. */
export function formatDay(milliseconds: number, offsetMilliseconds: number): string {
  return shiftedTime(milliseconds, offsetMilliseconds).slice(5, 10).replace('-', '/')
}
