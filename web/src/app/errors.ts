import { ApiError } from './api'
import { apiErrorText } from './error-text'

// Client-side failure codes (status 0 or unreadable responses).
const clientTexts: Record<string, string> = {
  invalid_response: '服务器响应无效，请稍后重试',
  network_unavailable: '无法连接服务器，请检查网络后重试',
  timeout: '服务器响应超时，请稍后重试',
  config_revision_missing: '请重新登录后保存配置',
  github_batch_empty: '请至少填写一个 GitHub 仓库',
  github_batch_invalid: '请先修正预览表中标红的项',
}

/** Returns a client-side failure (status 0) with a code from clientTexts. */
export function clientError(code: string): ApiError {
  return new ApiError(code.replaceAll('_', ' '), { status: 0, code, requestId: '' })
}

/**
 * Returns the console text of a failure: the caller's code table first, then
 * the server code catalog, then a text for the HTTP status. An aborted request
 * has no text.
 */
export function errorText(error: unknown, messages: Record<string, string> = {}): string {
  if (error instanceof DOMException && error.name === 'AbortError') return ''
  if (!(error instanceof ApiError)) return '操作失败，请重试'
  const own = messages[error.code] ?? clientTexts[error.code]
  if (own) return own
  const details =
    typeof error.details === 'object' && error.details !== null
      ? (error.details as Record<string, unknown>)
      : undefined
  return apiErrorText(error.code ? { code: error.code, details } : undefined, error.status)
}
