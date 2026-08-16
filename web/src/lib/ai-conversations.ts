// Conversation persistence for the AI Workspace — entirely client-side.
// The backend has no conversation-storage table; per ARCHITECTURE.md,
// conversations live only in the dashboard's own localStorage. This is
// the same honesty rule as collection-meta.ts / file-meta.ts: real state,
// never presented as server-synced, never shared across browsers/devices.
import { useSyncExternalStore } from "react"
import type { ChatAttachmentUploadResponse, ContextRefInput, ConversationExcerptInput, ExecutedAction, ProposedAction } from "@/lib/ai-types"

export interface AiMessage {
  id: string
  role: "user" | "assistant"
  content: string
  createdAt: number
  attachments?: ChatAttachmentUploadResponse[]
  contextRefs?: ContextRefInput[]
  conversationExcerpts?: ConversationExcerptInput[]
  actions?: ProposedAction[]
  // executedActions is the Activity Panel feed for this turn — real tool
  // calls that ran for real (see executedActionSummary server-side),
  // never a payload/JSON dump.
  executedActions?: ExecutedAction[]
  error?: string
  aborted?: boolean
  bookmarked?: boolean
}

export interface AiConversation {
  id: string
  title: string
  messages: AiMessage[]
  createdAt: number
  updatedAt: number
  pinned: boolean
  favorite: boolean
  archived: boolean
  folder: string | null
}

const STORAGE_KEY = "onebox_ai_conversations"

function read(): AiConversation[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as AiConversation[]) : []
  } catch {
    return []
  }
}

let cache: AiConversation[] = read()
const listeners = new Set<() => void>()

function write(next: AiConversation[]) {
  cache = next
  localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function newId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
}

export function listConversations(): AiConversation[] {
  return cache
}

export function getConversation(id: string): AiConversation | undefined {
  return cache.find((c) => c.id === id)
}

export function createConversation(title = "New chat"): AiConversation {
  const conv: AiConversation = {
    id: newId(),
    title,
    messages: [],
    createdAt: Date.now(),
    updatedAt: Date.now(),
    pinned: false,
    favorite: false,
    archived: false,
    folder: null,
  }
  write([conv, ...cache])
  return conv
}

function patchConversation(id: string, patch: Partial<AiConversation>) {
  write(cache.map((c) => (c.id === id ? { ...c, ...patch, updatedAt: Date.now() } : c)))
}

export function renameConversation(id: string, title: string) {
  patchConversation(id, { title })
}

export function deleteConversation(id: string) {
  write(cache.filter((c) => c.id !== id))
}

export function duplicateConversation(id: string): AiConversation | undefined {
  const src = getConversation(id)
  if (!src) return undefined
  const copy: AiConversation = {
    ...src,
    id: newId(),
    title: `${src.title} (copy)`,
    createdAt: Date.now(),
    updatedAt: Date.now(),
    pinned: false,
  }
  write([copy, ...cache])
  return copy
}

export function togglePin(id: string) {
  const c = getConversation(id)
  if (c) patchConversation(id, { pinned: !c.pinned })
}

export function toggleFavorite(id: string) {
  const c = getConversation(id)
  if (c) patchConversation(id, { favorite: !c.favorite })
}

export function toggleArchive(id: string) {
  const c = getConversation(id)
  if (c) patchConversation(id, { archived: !c.archived })
}

export function setFolder(id: string, folder: string | null) {
  patchConversation(id, { folder })
}

export function appendMessage(convId: string, message: AiMessage) {
  const c = getConversation(convId)
  if (!c) return
  const messages = [...c.messages, message]
  const title = c.messages.length === 0 && message.role === "user" ? deriveTitle(message.content) : c.title
  patchConversation(convId, { messages, title })
}

export function updateMessage(convId: string, messageId: string, patch: Partial<AiMessage>) {
  const c = getConversation(convId)
  if (!c) return
  patchConversation(convId, {
    messages: c.messages.map((m) => (m.id === messageId ? { ...m, ...patch } : m)),
  })
}

export function deleteMessage(convId: string, messageId: string) {
  const c = getConversation(convId)
  if (!c) return
  patchConversation(convId, { messages: c.messages.filter((m) => m.id !== messageId) })
}

/** Truncates to (and including) `uptoMessageId`, dropping everything after — used by edit/regenerate. */
export function truncateAfter(convId: string, uptoMessageId: string) {
  const c = getConversation(convId)
  if (!c) return
  const i = c.messages.findIndex((m) => m.id === uptoMessageId)
  if (i === -1) return
  patchConversation(convId, { messages: c.messages.slice(0, i + 1) })
}

export function deriveTitle(text: string): string {
  const clean = text.trim().replace(/\s+/g, " ")
  return clean.length > 48 ? clean.slice(0, 48) + "…" : clean || "New chat"
}

export function exportMarkdown(id: string): string {
  const c = getConversation(id)
  if (!c) return ""
  const lines = [`# ${c.title}`, ""]
  for (const m of c.messages) {
    lines.push(`### ${m.role === "user" ? "You" : "Assistant"}`, "", m.content, "")
  }
  return lines.join("\n")
}

export function exportJSON(id: string): string {
  const c = getConversation(id)
  return c ? JSON.stringify(c, null, 2) : "{}"
}

export function useConversations(): AiConversation[] {
  return useSyncExternalStore(subscribe, () => cache)
}

export function useConversation(id: string | null): AiConversation | undefined {
  const all = useConversations()
  return id ? all.find((c) => c.id === id) : undefined
}
