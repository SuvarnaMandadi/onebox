import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { PanelLeft, PanelRight, Sparkles } from "lucide-react"
import { Button } from "@/components/ui/button"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { ChatSidebar } from "@/components/ai/chat-sidebar"
import { ContextPanel } from "@/components/ai/context-panel"
import { Composer, type ComposerSendPayload } from "@/components/ai/composer"
import { MessageBubble } from "@/components/ai/message-bubble"
import { PROMPT_LIBRARY } from "@/lib/prompt-library"
import { streamChat } from "@/lib/ai-stream"
import {
  appendMessage,
  createConversation,
  deleteMessage,
  getConversation,
  truncateAfter,
  updateMessage,
  useConversation,
  useConversations,
  type AiMessage,
} from "@/lib/ai-conversations"
import type { ChatAttachmentRef, ChatHistoryTurn } from "@/lib/ai-types"

function newId(): string {
  return crypto.randomUUID()
}

export function AIWorkspacePage() {
  const conversations = useConversations()
  const [activeId, setActiveId] = useState<string | null>(null)
  const conv = useConversation(activeId)

  const [leftOpen, setLeftOpen] = useState(true)
  const [rightOpen, setRightOpen] = useState(true)
  const [streamingMsgId, setStreamingMsgId] = useState<string | null>(null)
  const [streamBuffer, setStreamBuffer] = useState("")
  const [quoteText, setQuoteText] = useState<string | undefined>(undefined)
  const [initialAttachIds, setInitialAttachIds] = useState<string[] | undefined>(undefined)

  const bufferRef = useRef("")
  const abortRef = useRef<AbortController | null>(null)
  const activeSendRef = useRef<{ convId: string; assistantId: string } | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Pick up the most recently updated conversation on first load, if any.
  useEffect(() => {
    if (activeId === null && conversations.length > 0) {
      setActiveId([...conversations].sort((a, b) => b.updatedAt - a.updatedAt)[0].id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const [searchParams, setSearchParams] = useSearchParams()

  // Entry points from the command palette / Files workspace: ?action=new
  // (New Chat), ?conversation=<id> (open a specific chat), ?prefill=<text>
  // (start fresh with the composer pre-filled — prompt-template picks,
  // the "Ask AI" search fallback), ?attach=<id,id,...> (Files' "Compare
  // with AI" bulk action — pre-stages those files as attachments). Runs
  // once on mount, after the most-recent-conversation pickup above, so it
  // wins when both apply.
  useEffect(() => {
    const action = searchParams.get("action")
    const conversationId = searchParams.get("conversation")
    const prefill = searchParams.get("prefill")
    const attach = searchParams.get("attach")
    if (action === "new") {
      setActiveId(null)
      setQuoteText(undefined)
    } else if (conversationId && getConversation(conversationId)) {
      setActiveId(conversationId)
    } else if (prefill) {
      setActiveId(null)
      setQuoteText(prefill)
    }
    if (attach) {
      setActiveId(null)
      setInitialAttachIds(attach.split(",").filter(Boolean))
    }
    if (action || conversationId || prefill || attach) {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        next.delete("action")
        next.delete("conversation")
        next.delete("prefill")
        next.delete("attach")
        return next
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" })
  }, [conv?.messages.length, streamBuffer])

  function finalizeStream(patch: Partial<AiMessage>) {
    const active = activeSendRef.current
    if (!active) return
    updateMessage(active.convId, active.assistantId, { content: bufferRef.current, ...patch })
    activeSendRef.current = null
    bufferRef.current = ""
    setStreamingMsgId(null)
    setStreamBuffer("")
    abortRef.current = null
  }

  const runTurn = useCallback((convId: string) => {
    const c = getConversation(convId)
    if (!c || c.messages.length === 0) return
    const last = c.messages[c.messages.length - 1]
    const history: ChatHistoryTurn[] = c.messages.slice(0, -1).map((m) => ({
      role: m.role,
      content: m.content,
      attachments: m.attachments?.map((a): ChatAttachmentRef => ({ id: a.id, filename: a.filename, kind: a.kind })),
    }))

    const assistantId = newId()
    appendMessage(convId, { id: assistantId, role: "assistant", content: "", createdAt: Date.now() })
    activeSendRef.current = { convId, assistantId }
    bufferRef.current = ""
    setStreamBuffer("")
    setStreamingMsgId(assistantId)

    const controller = new AbortController()
    abortRef.current = controller

    streamChat(
      {
        message: last.content,
        history,
        attachment_ids: last.attachments?.map((a) => a.id),
        context_refs: last.contextRefs,
        context: { page: "ai" },
      },
      {
        onDelta: (chunk) => {
          bufferRef.current += chunk
          setStreamBuffer(bufferRef.current)
        },
        onDone: (actions, executedActions) => finalizeStream({ actions, executedActions }),
        onError: (message) => finalizeStream({ error: message }),
      },
      controller.signal,
    )
  }, [])

  function handleNew() {
    setActiveId(null)
    setQuoteText(undefined)
  }

  function handleSend(payload: ComposerSendPayload) {
    let convId = activeId
    if (!convId) {
      const c = createConversation()
      convId = c.id
      setActiveId(convId)
    }
    appendMessage(convId, {
      id: newId(),
      role: "user",
      content: payload.text,
      createdAt: Date.now(),
      attachments: payload.attachments.length ? payload.attachments : undefined,
      contextRefs: payload.contextRefs.length ? payload.contextRefs : undefined,
    })
    setQuoteText(undefined)
    runTurn(convId)
  }

  function handleStop() {
    abortRef.current?.abort()
    finalizeStream({ aborted: true })
    toast.info("Stopped generating")
  }

  function handleEdit(convId: string, messageId: string, newText: string) {
    updateMessage(convId, messageId, { content: newText })
    truncateAfter(convId, messageId)
    runTurn(convId)
  }

  function handleRegenerate(convId: string, assistantMsgId: string) {
    const c = getConversation(convId)
    if (!c) return
    const idx = c.messages.findIndex((m) => m.id === assistantMsgId)
    const priorUser = [...c.messages.slice(0, idx)].reverse().find((m) => m.role === "user")
    if (!priorUser) return
    truncateAfter(convId, priorUser.id)
    runTurn(convId)
  }

  const displayMessages = useMemo(() => {
    if (!conv) return []
    if (!streamingMsgId) return conv.messages
    return conv.messages.map((m) => (m.id === streamingMsgId ? { ...m, content: streamBuffer } : m))
  }, [conv, streamingMsgId, streamBuffer])

  const isStreaming = !!streamingMsgId

  return (
    <div className="-m-6 h-[calc(100vh-3.5rem)]">
      <ResizablePanelGroup orientation="horizontal">
        {leftOpen && (
          <>
            <ResizablePanel defaultSize="18" minSize="14" maxSize="30">
              <ChatSidebar activeId={activeId} onSelect={setActiveId} onNew={handleNew} />
            </ResizablePanel>
            <ResizableHandle />
          </>
        )}

        <ResizablePanel defaultSize={rightOpen ? "60" : "82"} minSize="40">
          <div className="flex h-full flex-col">
            <div className="flex items-center gap-2 border-b px-3 py-2">
              <Button variant="ghost" size="icon" className="size-7" onClick={() => setLeftOpen((v) => !v)} aria-label="Toggle chat list">
                <PanelLeft className="size-4" />
              </Button>
              <p className="flex-1 truncate text-sm font-medium">{conv?.title ?? "AI Workspace"}</p>
              <Button variant="ghost" size="icon" className="size-7" onClick={() => setRightOpen((v) => !v)} aria-label="Toggle context panel">
                <PanelRight className="size-4" />
              </Button>
            </div>

            <div ref={scrollRef} className="flex-1 overflow-y-auto">
              {!conv || conv.messages.length === 0 ? (
                <WelcomeScreen onPick={(text) => handleSend({ text, attachments: [], contextRefs: [] })} />
              ) : (
                <div className="mx-auto max-w-3xl py-4">
                  {displayMessages.map((m) => (
                    <MessageBubble
                      key={m.id}
                      message={m}
                      streaming={m.id === streamingMsgId}
                      onRegenerate={m.role === "assistant" && !isStreaming && conv ? () => handleRegenerate(conv.id, m.id) : undefined}
                      onEdit={m.role === "user" && !isStreaming && conv ? (text) => handleEdit(conv.id, m.id, text) : undefined}
                      onDelete={!isStreaming && conv ? () => deleteMessage(conv.id, m.id) : undefined}
                      onQuote={(text) => setQuoteText(`> ${text.split("\n").join("\n> ")}\n\n`)}
                      onToggleBookmark={conv ? () => updateMessage(conv.id, m.id, { bookmarked: !m.bookmarked }) : undefined}
                    />
                  ))}
                </div>
              )}
            </div>

            <div className="mx-auto w-full max-w-3xl">
              <Composer
                disabled={isStreaming}
                streaming={isStreaming}
                onSend={handleSend}
                onStop={handleStop}
                initialText={quoteText}
                initialAttachmentIds={initialAttachIds}
              />
            </div>
          </div>
        </ResizablePanel>

        {rightOpen && (
          <>
            <ResizableHandle />
            <ResizablePanel defaultSize="22" minSize="16" maxSize="35">
              <ContextPanel conversation={conv} />
            </ResizablePanel>
          </>
        )}
      </ResizablePanelGroup>
    </div>
  )
}

function WelcomeScreen({ onPick }: { onPick: (text: string) => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-6 px-6 text-center">
      <div className="flex size-12 items-center justify-center rounded-2xl bg-primary text-primary-foreground">
        <Sparkles className="size-6" />
      </div>
      <div>
        <h2 className="text-lg font-semibold">How can I help with your workspace?</h2>
        <p className="text-sm text-muted-foreground">Ask about your collections, records, and files — or start from a prompt.</p>
      </div>
      <div className="grid w-full max-w-lg grid-cols-1 gap-2 sm:grid-cols-2">
        {PROMPT_LIBRARY.slice(0, 6).map((p) => (
          <button
            key={p.id}
            onClick={() => onPick(p.prompt)}
            className="rounded-lg border p-3 text-left text-sm transition-colors hover:bg-accent"
          >
            {p.label}
          </button>
        ))}
      </div>
    </div>
  )
}
