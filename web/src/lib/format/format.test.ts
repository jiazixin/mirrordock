import { describe, expect, it } from 'vitest'
import { formatDiskSize, formatSize } from './bytes'
import { formatServerTime } from './date'
import { formatDuration } from './duration'
import { formatNumber, formatPercent, formatRate } from './number'

describe('format', () => {
  it('formats sizes', () => {
    expect(formatSize(0)).toBe('0 B')
    expect(formatSize(1536)).toBe('1.5 KiB')
    expect(formatDiskSize(2 * 1024 ** 4)).toBe('2.00 TiB')
  })

  it('formats durations', () => {
    expect(formatDuration(null)).toBe('—')
    expect(formatDuration(45)).toBe('45s')
    expect(formatDuration(200)).toBe('3m20s')
    expect(formatDuration(8100)).toBe('2h15m')
  })

  it('formats numbers and rates', () => {
    expect(formatNumber(12345.67)).toBe('12,345.7')
    expect(formatRate(1, 3)).toBe('33.3')
    expect(formatPercent(42.25, 0)).toBe('42%')
  })

  it('formats server times in the server zone or by offset', () => {
    expect(formatServerTime('0001-01-01T00:00:00Z', {})).toBe('—')
    expect(formatServerTime('2026-10-26T08:00:00Z', { timezone: 'Asia/Shanghai' })).toBe(
      '2026/10/26 16:00:00',
    )
    expect(formatServerTime('2026-10-26T08:00:00Z', { utcOffsetSeconds: 3600 })).toBe(
      '2026-10-26 09:00:00',
    )
  })
})
