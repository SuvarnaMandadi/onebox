// Streaming client for POST /api/chat with Accept: text/event-stream —
// the exact SSE contract streamChatReply already implements
// (internal/server/chatbot_handlers.go): a sequence of
// `data: {"delta": "..."}` events followed by one
// `data: {"done": true, "actions"?: [...]}` (or `{"error": true}`).
// EventSource can't be used here since it doesn't support POST bodies —
// this reads the fetch response body stream directly instead. No new
// backend surface; this is the same endpoint the non-streaming client in
// api.ts already calls, just consumed incrementally.
import { getToken } from "@/lib/api"
import type { ChatbotRequestBody, ExecutedAction, ProposedAction } from "@/lib/ai-types"

export interface StreamHandlers {
  onDelta: (chunk: string) => void
  onDone: (actions: ProposedAction[] | undefined, executedActions: ExecutedAction[] | undefined) => void
  onError: (message: string) => void
}

export async function streamChat(body: ChatbotRequestBody, handlers: StreamHandlers, signal: AbortSignal): Promise<void> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "text/event-stream",
  }
  const token = getToken()
  if (token) headers["Authorization"] = `Bearer ${token}`

  let res: Response
  try {
    res = await fetch("/api/chat", { method: "POST", headers, body: JSON.stringify(body), signal })
  } catch (err) {
    if ((err as Error).name === "AbortError") return
    handlers.onError("Network error — couldn't reach the server.")
    return
  }

  if (!res.ok || !res.body) {
    let message = res.statusText || "Request failed"
    try {
      const data = await res.json()
      if (data?.message) message = data.message
    } catch {
      /* non-JSON error body */
    }
    handlers.onError(message)
    return
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""

  try {
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
        let evt: { delta?: string; done?: boolean; actions?: ProposedAction[]; executed_actions?: ExecutedAction[]; error?: boolean }
        try {
          evt = JSON.parse(json)
        } catch {
          continue
        }
        if (evt.error) {
          handlers.onError("The AI request failed. Please try again.")
          return
        }
        if (typeof evt.delta === "string") handlers.onDelta(evt.delta)
        if (evt.done) {
          handlers.onDone(evt.actions, evt.executed_actions)
          return
        }
      }
    }
    // The stream closed (a clean EOF from the reader) without ever sending
    // a `done`/`error` SSE frame — a proxy timeout, backend crash, or
    // flaky connection. Without this, the caller's onDone/onError never
    // fires and the UI is left showing a "thinking" state forever.
    handlers.onError("Connection closed before a response was received.")
  } catch (err) {
    if ((err as Error).name === "AbortError") return
    handlers.onError("Connection lost while streaming the response.")
  }
}
