// Wire types for the existing chat pipeline (internal/server/chatbot_handlers.go,
// chatbot_context_refs.go, chatbot_actions.go) — this milestone adds no new
// backend endpoints, only a client for the ones Milestones 1/2 already
// proved out.
import type { Field } from "@/lib/types"

export interface ContextRefInput {
  type: "collection" | "record"
  collection: string
  record_id?: string
}

export interface ConversationExcerptInput {
  title: string
  text: string
}

export interface ChatHistoryTurn {
  role: "user" | "assistant"
  content: string
  attachments?: ChatAttachmentRef[]
}

export interface ChatAttachmentRef {
  id: string
  filename: string
  kind: "image" | "document"
}

export interface ProposedAction {
  id: string
  type: string
  title: string
  description: string
  payload?: Record<string, unknown>
  destructive: boolean
}

// ExecutedAction is the Activity Panel feed (Milestone 5) — Type/Title
// only, mirroring executedActionSummary (internal/server/chatbot_tool_execution.go).
// Deliberately never carries a payload: this is what actually ran, not a
// proposal, and the UI must never render provider/tool-call JSON for it.
export interface ExecutedAction {
  type: string
  title: string
  description: string
}

export interface ChatbotRequestBody {
  message: string
  context?: { page?: string; collection?: string; record_id?: string }
  history?: ChatHistoryTurn[]
  attachment_ids?: string[]
  context_refs?: ContextRefInput[]
  conversation_excerpts?: ConversationExcerptInput[]
}

export interface ChatbotResponseBody {
  reply: string
  actions?: ProposedAction[]
  executed_actions?: ExecutedAction[]
}

export interface ChatAttachmentUploadResponse {
  id: string
  filename: string
  mime: string
  size: number
  kind: "image" | "document"
  created: string
  extraction_error?: string
}

export interface CollectionSummary {
  name: string
  fields: Field[]
}
