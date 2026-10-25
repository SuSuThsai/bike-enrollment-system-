const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) || '/api/v1'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId?: string

  constructor(status: number, code: string, message: string, requestId?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
  }
}

export function buildQuery(params: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      search.set(key, String(value))
    }
  })
  const query = search.toString()
  return query ? `?${query}` : ''
}

async function buildError(response: Response): Promise<ApiError> {
  let code = `HTTP_${response.status}`
  let message = response.statusText || '请求失败'
  let requestId: string | undefined
  try {
    const body = (await response.json()) as {
      code?: string
      message?: string
      request_id?: string
    }
    code = body.code || code
    message = body.message || message
    requestId = body.request_id
  } catch {
    // 非 JSON 错误响应保留 HTTP 状态信息。
  }
  return new ApiError(response.status, code, message, requestId)
}

export async function request<T>(path: string): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    throw await buildError(response)
  }
  return (await response.json()) as T
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message
  }
  return error instanceof Error ? error.message : '请求失败'
}
