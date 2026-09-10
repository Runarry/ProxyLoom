import type { components } from './schema'

export type Schema<K extends keyof components['schemas']> = components['schemas'][K]
export type APIResult<T> = { body: T; etag: string | null }

const messages: Record<string, string> = {
  AUTH_REQUIRED: '请登录后继续。', SESSION_EXPIRED: '会话已过期，请重新登录。',
  AUTH_FAILED: '认证失败，请检查凭据。', PERMISSION_DENIED: '操作被拒绝，请检查权限和访问地址。',
  REAUTH_REQUIRED: '此操作需要再次验证管理员密码。', RESOURCE_NOT_FOUND: '资源不存在或已被删除。',
  REVISION_MISMATCH: '资源已被其他操作修改。当前草稿已保留，请加载最新版本进行比较。',
  PRECONDITION_REQUIRED: '缺少修订前置条件，请重新读取资源。',
  VALIDATION_FAILED: '内容不符合要求，请检查标出的字段。', MALFORMED_REQUEST: '请求格式不正确。',
  INPUT_LIMIT_EXCEEDED: '输入超过服务端限制，请缩小输入后重试。',
  STATE_CONFLICT: '当前状态不允许此操作，请刷新状态后检查。',
  IDEMPOTENCY_CONFLICT: '该提交标识已用于不同请求，请检查提交结果。',
  RATE_LIMITED: '操作过于频繁，请稍后重试。', SERVICE_UNAVAILABLE: '服务暂时不可用，请稍后重试。',
  INTERNAL_ERROR: '服务处理失败，请根据请求编号排查。',
  CAPABILITY_UNSUPPORTED: '此配置不受支持。', CAPABILITY_UNVERIFIED: '此配置尚未完成内核兼容验证。',
  DNS_CYCLE: 'DNS 与出站存在循环依赖，请检查解析器、引导及出站引用。',
  DEPENDENCY_CYCLE: '资源之间存在循环依赖，请检查标出的引用。', DEPENDENCY_EXCLUDED: '引用资源被显式排除，请检查依赖配置。',
  CLIPBOARD_UNAVAILABLE: '无法访问剪贴板，请检查浏览器权限或手动选择复制。',
}

export class APIError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    public readonly requestId = '',
    public readonly details: Schema<'ErrorDetail'>[] = [],
    public readonly retryAfter: string | null = null,
  ) {
    super(messages[code] ?? (status === 0 ? '无法连接服务，请检查网络后重试。' : '请求失败，请根据错误代码和请求编号排查。'))
    this.name = 'APIError'
  }
}

let csrfToken = ''
let onUnauthorized: (() => void) | undefined
export function configureSession(token: string, unauthorized?: () => void) {
  csrfToken = token
  if (unauthorized) onUnauthorized = unauthorized
}

type Options = {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  body?: unknown
  etag?: string
  idempotencyKey?: string
  signal?: AbortSignal
  anonymous?: boolean
  preserveSessionOn401?: boolean
}

// The cookie, CSRF token and all sensitive drafts stay in this browser session's memory.
// Never log request bodies or automatically replay a mutation.
export async function api<T>(path: string, options: Options = {}): Promise<APIResult<T>> {
  const method = options.method ?? 'GET'
  const headers = new Headers({ Accept: 'application/json' })
  const multipart = options.body instanceof FormData
  if (method !== 'GET') {
    if (!multipart) headers.set('Content-Type', 'application/json')
    if (!options.anonymous && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  }
  if (options.etag) headers.set('If-Match', options.etag)
  if (options.idempotencyKey) headers.set('Idempotency-Key', options.idempotencyKey)
  let response: Response
  try {
    response = await fetch(`/api/v1${path}`, {
      method, headers, credentials: 'same-origin', cache: 'no-store', signal: options.signal,
      body: options.body === undefined ? undefined : multipart ? options.body as FormData : JSON.stringify(options.body),
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new APIError(0, 'NETWORK_ERROR')
  }
  let body: T | Schema<'ErrorResponse'>
  try { body = await response.json() } catch { throw new APIError(response.status, 'INVALID_RESPONSE', response.headers.get('X-Request-ID') ?? '') }
  if (!response.ok) {
    const failed = body as Schema<'ErrorResponse'>
    if (response.status === 401 && !options.anonymous && !options.preserveSessionOn401 && failed.error?.code !== 'AUTH_FAILED') onUnauthorized?.()
    throw new APIError(response.status, failed.error?.code ?? 'INVALID_RESPONSE', failed.request_id ?? '', failed.error?.details ?? [], response.headers.get('Retry-After'))
  }
  return { body: body as T, etag: response.headers.get('ETag') }
}

export function revisionTag(revision: string) { return `"r${revision}"` }
export function queryString(values: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(values)) if (value !== undefined && value !== '') query.set(key, String(value))
  const result = query.toString()
  return result ? `?${result}` : ''
}
export function errorMessage(error: unknown): string {
  return error instanceof APIError ? error.message : error instanceof Error && ['DraftError', 'NetworkFormError'].includes(error.name) ? error.message : '操作未完成，请重试。'
}
export function localTime(value: string | undefined) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(date)
}
