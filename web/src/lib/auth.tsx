import { createContext, useContext, useState, useCallback, useEffect, type ReactNode } from "react"
import { api, getToken, setToken, clearToken, UNAUTHORIZED_EVENT } from "@/lib/api"
import { clearSearchHistory } from "@/lib/search-history"

interface UnifiedLoginResponse {
  token: string
  role: "admin" | "user"
}

interface AuthState {
  isAuthenticated: boolean
  role: "admin" | "user" | null
  loading: boolean
  login: (email: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [role, setRole] = useState<"admin" | "user" | null>(
    getToken() ? (localStorage.getItem("onebox_role") as "admin" | "user" | null) : null,
  )
  const [loading, setLoading] = useState(false)

  const login = useCallback(async (email: string, password: string) => {
    setLoading(true)
    try {
      const res = await api.post<UnifiedLoginResponse>("/api/login", { email, password })
      setToken(res.token)
      localStorage.setItem("onebox_role", res.role)
      setRole(res.role)
    } finally {
      setLoading(false)
    }
  }, [])

  // Shared by an explicit "Log out" click and an automatic one (a 401 from
  // any request — see UNAUTHORIZED_EVENT below) — both need to end up in
  // the exact same clean state, not two copies of "clear everything" that
  // could drift out of sync.
  const clearSession = useCallback(() => {
    clearToken()
    localStorage.removeItem("onebox_role")
    // Local search history has no per-user namespacing; leaving it behind
    // on logout would show the next person on a shared machine whatever
    // the previous account searched for.
    clearSearchHistory()
    setRole(null)
  }, [])

  const logout = useCallback(() => {
    clearSession()
  }, [clearSession])

  // A stale/expired/revoked token surfaces as a 401 on whatever request
  // happens to hit it first — without this, isAuthenticated (derived from
  // token *presence*, not validity) stayed true, so the sidebar/header
  // kept rendering as logged-in while individual pages failed silently
  // with their own inline error text. This brings the whole app back to
  // a real logged-out state as soon as any request discovers the session
  // is gone, instead of requiring the admin to notice and log out by hand.
  useEffect(() => {
    window.addEventListener(UNAUTHORIZED_EVENT, clearSession)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, clearSession)
  }, [clearSession])

  return (
    <AuthContext.Provider
      value={{ isAuthenticated: !!getToken() && !!role, role, loading, login, logout }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used within AuthProvider")
  return ctx
}
