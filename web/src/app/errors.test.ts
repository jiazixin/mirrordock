import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { errorText } from './errors'

const failure = (status: number, code: string, details?: unknown) =>
  new ApiError('failure', { status, code, requestId: '', details })

describe('errorText', () => {
  it('prefers the caller code table', () => {
    expect(errorText(failure(409, 'source_busy'), { source_busy: '自定义文案' })).toBe('自定义文案')
  })

  it('maps server codes with details', () => {
    expect(errorText(failure(503, 'log_cleanup_failed', { cleared: 2 }))).toContain('2')
  })

  it('falls back to the status for unknown codes', () => {
    expect(errorText(failure(502, 'something_new'))).toBe('请求失败（HTTP 502）')
  })

  it('has no text for an aborted request', () => {
    expect(errorText(new DOMException('aborted', 'AbortError'))).toBe('')
  })

  it('rejects malformed validation details without rendering them', () => {
    for (const field of [null, 'invalid', { path: {}, reason: 'required' }]) {
      expect(errorText(failure(400, 'invalid_argument', { fields: [field] }))).toBe('请求参数无效')
    }
  })
})
