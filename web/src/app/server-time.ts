import { formatServerTime } from '../lib/format/date'
import { state } from './state'

/** A server timestamp in the server's configured time zone. */
export function time(value?: string): string {
  return formatServerTime(value, {
    timezone: state.info?.timezone,
    utcOffsetSeconds: state.info?.utc_offset_seconds,
  })
}
