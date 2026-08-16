// Thin client for the Settings control-center endpoints
// (internal/server/diagnostics_handlers.go, backend_health.go). Every
// function here is a direct call to a real backend endpoint — this file
// deliberately contains no provider logic of its own (no fetch to
// Ollama/Anthropic/OpenAI directly), matching Section 11's architecture
// requirement that provider checks live in the backend, never the
// frontend.
import { api, getToken } from "@/lib/api"
import type {
  BackendHealthReport,
  ConfigIssue,
  DiagnosticsHistoryEntry,
  OllamaPullProgress,
  PerformanceSummary,
  ProviderDiagnosticsReport,
} from "@/lib/diagnostics-types"

export interface DiagnosticsRequest {
  provider: "ollama" | "anthropic" | "openai" | "embedding"
  base_url?: string
  api_key?: string
  model?: string
  embedding_model?: string
  deep?: boolean
}

export function runDiagnostics(req: DiagnosticsRequest) {
  return api.post<ProviderDiagnosticsReport>("/api/settings/diagnostics", req)
}

export function getDiagnosticsHistory(provider?: string, limit = 50) {
  const qs = new URLSearchParams()
  if (provider) qs.set("provider", provider)
  qs.set("limit", String(limit))
  // items is a Go slice that serializes as JSON null when there's no
  // history yet for this provider (true for every provider on a fresh
  // install) — never assume it's an array.
  return api.get<{ items: DiagnosticsHistoryEntry[] | null }>(`/api/settings/diagnostics/history?${qs}`)
}

export function getPerformanceSummary(provider: string) {
  return api.get<PerformanceSummary>(`/api/settings/performance?provider=${encodeURIComponent(provider)}`)
}

export function validateSettings(candidate: Record<string, string>) {
  return api.post<{ issues: ConfigIssue[]; valid: boolean }>("/api/settings/validate", candidate)
}

export function getBackendHealth() {
  return api.get<BackendHealthReport>("/api/settings/backend-health")
}

// streamOllamaPull mirrors ai-stream.ts's streamChat SSE-consumption
// pattern against POST /api/settings/ollama-pull — the real download
// progress Ollama's own daemon reports, relayed frame-by-frame, not a
// synthesized progress bar (see llm.OllamaClient.Pull's doc comment).
export async function streamOllamaPull(
  baseURL: string | undefined,
  model: string,
  onProgress: (p: OllamaPullProgress) => void,
  signal: AbortSignal,
): Promise<void> {
  const headers: Record<string, string> = { "Content-Type": "application/json" }
  const token = getToken()
  if (token) headers["Authorization"] = `Bearer ${token}`

  const res = await fetch("/api/settings/ollama-pull", {
    method: "POST",
    headers,
    body: JSON.stringify({ base_url: baseURL, model }),
    signal,
  })
  if (!res.ok || !res.body) {
    let message = res.statusText || "Pull failed to start"
    try {
      const data = await res.json()
      if (data?.message) message = data.message
    } catch {
      /* non-JSON error body */
    }
    onProgress({ status: "error", error: message })
    return
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let sep: number
    while ((sep = buffer.indexOf("\n\n")) !== -1) {
      const frame = buffer.slice(0, sep)
      buffer = buffer.slice(sep + 2)
      const line = frame.split("\n").find((l) => l.startsWith("data:"))
      if (!line) continue
      const json = line.slice(5).trim()
      if (!json) continue
      try {
        onProgress(JSON.parse(json) as OllamaPullProgress)
      } catch {
        /* malformed frame — skip */
      }
    }
  }
}
