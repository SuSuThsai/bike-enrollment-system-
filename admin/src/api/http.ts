export const ADMIN_TOKEN_KEY = 'bike_admin_token'

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

export function getToken(): string {
  return localStorage.getItem(ADMIN_TOKEN_KEY) || ''
}

export function setToken(token: string): void {
  if (token) {
    localStorage.setItem(ADMIN_TOKEN_KEY, token)
  } else {
    localStorage.removeItem(ADMIN_TOKEN_KEY)
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

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  const response = await fetch(`${API_BASE}${path}`, { ...init, headers })
  if (!response.ok) {
    throw await buildError(response)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

export interface DownloadFile {
  blob: Blob
  filename: string
}

export async function requestBlob(path: string, init: RequestInit = {}): Promise<DownloadFile> {
  const headers = new Headers(init.headers)
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  const response = await fetch(`${API_BASE}${path}`, { ...init, headers })
  if (!response.ok) {
    throw await buildError(response)
  }

  const disposition = response.headers.get('Content-Disposition') || ''
  const filenameMatch = disposition.match(/filename="?([^";]+)"?/)
  return {
    blob: await response.blob(),
    filename: filenameMatch?.[1] || 'enrollments.xlsx',
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    const request = error.requestId ? `（request_id: ${error.requestId}）` : ''
    return `${error.message}${request}`
  }
  return error instanceof Error ? error.message : '请求失败'
}
