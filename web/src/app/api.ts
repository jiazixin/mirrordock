export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message)
  }
}
let sessionController = new AbortController()

export function cancelSessionRequests() {
  sessionController.abort()
  sessionController = new AbortController()
}

export async function api<T>(
  path: string,
  method = 'GET',
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const session = sessionController
  const requestSignal = signal ? AbortSignal.any([signal, session.signal]) : session.signal
  const res = await fetch(`/api${path}`, {
    method,
    signal: requestSignal,
    credentials: 'same-origin',
    headers: {
      'X-MirrorDock-Request': '1',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  requestSignal.throwIfAborted()
  if (res.status === 401 && !path.startsWith('/auth'))
    window.dispatchEvent(new Event('session-expired'))
  let data
  try {
    data = await res.json()
  } catch {
    requestSignal.throwIfAborted()
    throw new ApiError(`服务器响应无效（HTTP ${res.status}），请稍后重试`, res.status)
  }
  requestSignal.throwIfAborted()
  if (!res.ok) {
    throw new ApiError(data?.error || `HTTP ${res.status}`, res.status)
  }
  return data as T
}
