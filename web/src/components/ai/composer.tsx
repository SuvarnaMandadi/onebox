import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import {
  Database,
  FileText,
  Loader2,
  Mic,
  Paperclip,
  Send,
  Sparkles,
  Square,
  X,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useCollections } from "@/lib/collections-store"
import { useFiles } from "@/lib/files-store"
import { getToken } from "@/lib/api"
import { PROMPT_LIBRARY } from "@/lib/prompt-library"
import type { ChatAttachmentUploadResponse, ContextRefInput } from "@/lib/ai-types"
import type { RecordRow } from "@/lib/types"

async function uploadChatAttachment(file: File): Promise<ChatAttachmentUploadResponse> {
  const form = new FormData()
  form.append("file", file)
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch("/api/chat-attachments", { method: "POST", headers, body: form })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new Error(data?.message || "Upload failed")
  }
  return res.json()
}

interface StagedFile extends ChatAttachmentUploadResponse {
  uploading?: boolean
  error?: string
  clientId: string
}

export interface ComposerSendPayload {
  text: string
  attachments: ChatAttachmentUploadResponse[]
  contextRefs: ContextRefInput[]
}

export function Composer({
  disabled,
  streaming,
  onSend,
  onStop,
  initialText,
  initialAttachmentIds,
}: {
  disabled?: boolean
  streaming?: boolean
  onSend: (payload: ComposerSendPayload) => void
  onStop?: () => void
  initialText?: string
  /** File IDs to pre-stage as attachments — e.g. the Files workspace's "Compare with AI" bulk action. Resolved from the already-cached files-store, no extra fetch. */
  initialAttachmentIds?: string[]
}) {
  const [text, setText] = useState("")
  const [files, setFiles] = useState<StagedFile[]>([])
  const [refs, setRefs] = useState<ContextRefInput[]>([])
  const [mentionOpen, setMentionOpen] = useState(false)
  const [mentionCollection, setMentionCollection] = useState<string | null>(null)
  const [slashOpen, setSlashOpen] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const { collections } = useCollections()
  const { files: allFiles } = useFiles()

  useEffect(() => {
    if (initialText !== undefined) {
      setText(initialText)
      textareaRef.current?.focus()
    }
  }, [initialText])

  useEffect(() => {
    if (!initialAttachmentIds?.length || !allFiles) return
    const staged: StagedFile[] = initialAttachmentIds
      .map((id) => allFiles.find((f) => f.id === id))
      .filter((f): f is NonNullable<typeof f> => !!f)
      .map((f) => ({
        clientId: f.id,
        id: f.id,
        filename: f.filename,
        mime: f.mime,
        size: f.size,
        kind: f.mime.startsWith("image/") ? "image" : "document",
        created: f.created,
      }))
    if (staged.length) setFiles((prev) => [...prev, ...staged.filter((s) => !prev.some((p) => p.id === s.id))])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialAttachmentIds, allFiles])

  useEffect(() => {
    const ta = textareaRef.current
    if (!ta) return
    ta.style.height = "auto"
    ta.style.height = `${Math.min(ta.scrollHeight, 240)}px`
  }, [text])

  async function stageFiles(fileList: File[]) {
    const staged: StagedFile[] = fileList.map((f) => ({
      clientId: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
      id: "",
      filename: f.name,
      mime: f.type,
      size: f.size,
      kind: f.type.startsWith("image/") ? "image" : "document",
      created: "",
      uploading: true,
    }))
    setFiles((prev) => [...prev, ...staged])
    for (const [i, f] of fileList.entries()) {
      try {
        const rec = await uploadChatAttachment(f)
        setFiles((prev) => prev.map((s) => (s.clientId === staged[i].clientId ? { ...rec, clientId: s.clientId } : s)))
      } catch (err) {
        setFiles((prev) =>
          prev.map((s) =>
            s.clientId === staged[i].clientId ? { ...s, uploading: false, error: err instanceof Error ? err.message : "Failed" } : s,
          ),
        )
      }
    }
  }

  function removeFile(clientId: string) {
    setFiles((prev) => prev.filter((f) => f.clientId !== clientId))
  }

  function addRef(ref: ContextRefInput) {
    setRefs((prev) => (prev.some((r) => r.type === ref.type && r.collection === ref.collection && r.record_id === ref.record_id) ? prev : [...prev, ref]))
    setMentionOpen(false)
    setMentionCollection(null)
  }

  function removeRef(i: number) {
    setRefs((prev) => prev.filter((_, idx) => idx !== i))
  }

  function send() {
    const pendingUpload = files.some((f) => f.uploading)
    if (pendingUpload) {
      toast.info("Wait for attachments to finish uploading")
      return
    }
    const trimmed = text.trim()
    if (!trimmed && files.length === 0) return
    onSend({
      text: trimmed,
      attachments: files.filter((f) => f.id).map(({ id, filename, mime, size, kind, created, extraction_error }) => ({
        id, filename, mime, size, kind, created, extraction_error,
      })),
      contextRefs: refs,
    })
    setText("")
    setFiles([])
    setRefs([])
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      send()
    }
    if (e.key === "@") {
      // Without preventDefault, the "@" (and everything typed after it)
      // still lands in the textarea instead of the popover's own filter —
      // see the toolbar Database button, which opens this same popover
      // without ever letting a trigger character reach the textarea.
      e.preventDefault()
      setMentionOpen(true)
    }
    if (e.key === "/" && text.length === 0) {
      e.preventDefault()
      setSlashOpen(true)
    }
    if (e.key === "Escape") {
      setMentionOpen(false)
      setSlashOpen(false)
    }
  }

  const recordCandidates = useRecordCandidates(mentionCollection)

  return (
    <div className="space-y-2 border-t bg-background p-3">
      {(files.length > 0 || refs.length > 0) && (
        <div className="flex flex-wrap gap-1.5">
          {files.map((f) => (
            <Badge key={f.clientId} variant={f.error ? "destructive" : "secondary"} className="gap-1 py-1 font-normal">
              {f.uploading ? <Loader2 className="size-3 animate-spin" /> : <FileText className="size-3" />}
              {f.filename}
              <button type="button" onClick={() => removeFile(f.clientId)} aria-label={`Remove ${f.filename}`}>
                <X className="size-3" />
              </button>
            </Badge>
          ))}
          {refs.map((r, i) => (
            <Badge key={i} variant="outline" className="gap-1 py-1 font-normal">
              <Database className="size-3" />
              {r.type === "record" ? `${r.collection}#${r.record_id?.slice(0, 6)}` : r.collection}
              <button type="button" onClick={() => removeRef(i)} aria-label="Remove mention">
                <X className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}

      <div
        className="relative rounded-xl border bg-card focus-within:ring-1 focus-within:ring-ring"
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault()
          if (e.dataTransfer.files.length) stageFiles(Array.from(e.dataTransfer.files))
        }}
      >
        <Textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={(e) => {
            const imgs = Array.from(e.clipboardData.items)
              .filter((it) => it.kind === "file")
              .map((it) => it.getAsFile())
              .filter((f): f is File => !!f)
            if (imgs.length) stageFiles(imgs)
          }}
          placeholder="Ask anything… @ to mention, / for prompts"
          rows={1}
          className="max-h-60 min-h-11 resize-none border-0 shadow-none focus-visible:ring-0 dark:bg-transparent"
        />
        <div className="flex items-center justify-between px-2 pb-2">
          <div className="flex items-center gap-0.5">
            <input
              ref={fileInputRef}
              type="file"
              multiple
              className="sr-only"
              onChange={(e) => {
                if (e.target.files?.length) stageFiles(Array.from(e.target.files))
                e.target.value = ""
              }}
            />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" className="size-7" onClick={() => fileInputRef.current?.click()} aria-label="Attach files">
                  <Paperclip className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Attach files</TooltipContent>
            </Tooltip>

            <Popover
              open={mentionOpen}
              onOpenChange={(o) => {
                setMentionOpen(o)
                if (!o) setMentionCollection(null)
              }}
            >
              <PopoverTrigger asChild>
                <Button variant="ghost" size="icon" className="size-7" aria-label="Mention collection or record">
                  <Database className="size-4" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-72 p-0" align="start">
                <Command>
                  <CommandInput placeholder={mentionCollection ? `Search records in ${mentionCollection}…` : "Mention a collection or file…"} />
                  <CommandList>
                    <CommandEmpty>Nothing found.</CommandEmpty>
                    {!mentionCollection && (
                      <>
                        <CommandGroup heading="Collections">
                          {collections?.map((c) => (
                            <CommandItem key={c.id} onSelect={() => setMentionCollection(c.name)}>
                              <Database className="size-3.5" /> {c.name}
                            </CommandItem>
                          ))}
                        </CommandGroup>
                        <CommandGroup heading="Files">
                          {allFiles?.slice(0, 20).map((f) => (
                            <CommandItem
                              key={f.id}
                              onSelect={() => {
                                setFiles((prev) => [
                                  ...prev,
                                  { ...f, kind: f.mime.startsWith("image/") ? "image" : "document", clientId: f.id, created: f.created },
                                ])
                                setMentionOpen(false)
                              }}
                            >
                              <FileText className="size-3.5" /> {f.filename}
                            </CommandItem>
                          ))}
                        </CommandGroup>
                      </>
                    )}
                    {mentionCollection && (
                      <CommandGroup heading={mentionCollection}>
                        <CommandItem onSelect={() => addRef({ type: "collection", collection: mentionCollection })}>
                          Mention whole collection
                        </CommandItem>
                        {recordCandidates.map((r) => (
                          <CommandItem key={r.id} onSelect={() => addRef({ type: "record", collection: mentionCollection, record_id: r.id })}>
                            Record {r.id.slice(0, 8)}
                          </CommandItem>
                        ))}
                      </CommandGroup>
                    )}
                  </CommandList>
                </Command>
              </PopoverContent>
            </Popover>

            <Popover open={slashOpen} onOpenChange={setSlashOpen}>
              <PopoverTrigger asChild>
                <Button variant="ghost" size="icon" className="size-7" aria-label="Prompt templates">
                  <Sparkles className="size-4" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-72 p-0" align="start">
                <Command>
                  <CommandInput placeholder="Search prompts…" />
                  <CommandList>
                    <CommandEmpty>No prompts found.</CommandEmpty>
                    <CommandGroup heading="Prompt library">
                      {PROMPT_LIBRARY.map((p) => (
                        <CommandItem
                          key={p.id}
                          onSelect={() => {
                            setText(p.prompt)
                            setSlashOpen(false)
                            textareaRef.current?.focus()
                          }}
                        >
                          {p.label}
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  </CommandList>
                </Command>
              </PopoverContent>
            </Popover>

            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" className="size-7" disabled aria-label="Voice input (coming soon)">
                  <Mic className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Voice input — coming soon</TooltipContent>
            </Tooltip>
          </div>

          {streaming ? (
            <Button size="icon" className="size-8" variant="destructive" onClick={onStop} aria-label="Stop generating">
              <Square className="size-3.5 fill-current" />
            </Button>
          ) : (
            <Button size="icon" className="size-8" onClick={send} disabled={disabled} aria-label="Send message">
              <Send className="size-4" />
            </Button>
          )}
        </div>
      </div>
      <p className="px-1 text-[11px] text-muted-foreground">Enter to send · Shift+Enter for a new line</p>
    </div>
  )
}

function useRecordCandidates(collection: string | null): RecordRow[] {
  const [rows, setRows] = useState<RecordRow[]>([])
  useEffect(() => {
    if (!collection) {
      setRows([])
      return
    }
    let cancelled = false
    const token = getToken()
    fetch(`/api/collections/${collection}/records?limit=8`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
      .then((r) => r.json())
      .then((data) => !cancelled && setRows(data.items ?? []))
      .catch(() => !cancelled && setRows([]))
    return () => {
      cancelled = true
    }
  }, [collection])
  return rows
}
