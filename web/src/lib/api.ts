// Thin fetch wrapper for the onebox REST API. Relative URLs only — the
// Vite dev server proxies /api to the Go backend (see vite.config.ts),
// and in production this app is served BY that same backend, so "/api/..."
// always resolves correctly with no base-URL configuration needed.

const TOKEN_KEY = "onebox_token"

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

// Fired whenever any request comes back 401 — an expired/revoked token,
// most often. auth.tsx listens for this to clear its role state (which it
// owns) so `isAuthenticated` actually flips to false and RequireAuth
// redirects to /login. Without this, a stale token left every already-
// mounted page's own per-request error handling to fail independently
// while the sidebar/header kept rendering as if still logged in — the
// user had to notice and manually click "Log out" to recover.
export const UNAUTHORIZED_EVENT = "onebox:unauthorized"

function handleUnauthorized() {
  clearToken()
  window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
}

export class ApiError extends Error {
  status: number
  code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers["Authorization"] = `Bearer ${token}`
  if (body !== undefined) headers["Content-Type"] = "application/json"

  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  if (res.status === 204) return undefined as T

  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined

  if (!res.ok) {
    if (res.status === 401) handleUnauthorized()
    const message = data?.message || res.statusText || "Request failed"
    throw new ApiError(res.status, message, data?.code)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  delete: <T>(path: string) => request<T>("DELETE", path),
}

// GET /api/files/:id (and similar binary endpoints) require the same
// Bearer auth as everything else (see fileOwnerMatches — there's no
// "public file" concept, so a bare <img src="/api/files/:id"> can't carry
// the header and would 404). Fetch authenticated, hand back a Blob the
// caller turns into an object URL.
export async function fetchBlob(path: string): Promise<Blob> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers["Authorization"] = `Bearer ${token}`
  const res = await fetch(path, { headers })
  if (!res.ok) {
    if (res.status === 401) handleUnauthorized()
    let message = res.statusText || "Request failed"
    try {
      const data = await res.json()
      if (data?.message) message = data.message
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, message)
  }
  return res.blob()
}
