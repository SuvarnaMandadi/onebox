import { memo, useState } from "react"
import { toast } from "sonner"
import {
  Bookmark,
  Check,
  Copy,
  Database,
  FileText,
  Loader2,
  Pencil,
  Quote,
  RotateCw,
  Share2,
  ThumbsDown,
  ThumbsUp,
  Trash2,
  X,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { MarkdownRenderer } from "@/components/ai/markdown-renderer"
import { ProposalCard } from "@/components/ai/proposal-card"
import { ToolActivity } from "@/components/ai/tool-activity"
import { cn } from "@/lib/utils"
import type { AiMessage } from "@/lib/ai-conversations"

export const MessageBubble = memo(function MessageBubble({
  message,
  streaming,
  onRegenerate,
  onEdit,
  onDelete,
  onQuote,
  onToggleBookmark,
}: {
  message: AiMessage
  streaming?: boolean
  onRegenerate?: () => void
  onEdit?: (newText: string) => void
  onDelete?: () => void
  onQuote?: (text: string) => void
  onToggleBookmark?: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(message.content)
  const [copied, setCopied] = useState(false)
  const [reaction, setReaction] = useState<"up" | "down" | null>(null)
  const isUser = message.role === "user"

  function copy() {
    navigator.clipboard.writeText(message.content)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className={cn("group flex gap-3 px-4 py-3", isUser && "flex-row-reverse")}>
      <div
        className={cn(
          "flex min-w-0 max-w-[85%] flex-col gap-1.5",
          isUser && "items-end",
        )}
      >
        {(message.attachments?.length || message.contextRefs?.length || message.conversationExcerpts?.length) ? (
          <div className="flex flex-wrap gap-1.5">
            {message.attachments?.map((a) => (
              <Badge key={a.id} variant="outline" className="gap-1 font-normal">
                <FileText className="size-3" />
                {a.filename}
              </Badge>
            ))}
            {message.contextRefs?.map((r, i) => (
              <Badge key={i} variant="outline" className="gap-1 font-normal">
                <Database className="size-3" />
                {r.type === "record" ? `${r.collection}#${r.record_id?.slice(0, 6)}` : r.collection}
              </Badge>
            ))}
            {message.conversationExcerpts?.map((e, i) => (
              <Badge key={i} variant="outline" className="gap-1 font-normal">
                {e.title}
              </Badge>
            ))}
          </div>
        ) : null}

        {!isUser && !!message.executedActions?.length && <ToolActivity actions={message.executedActions} />}

        {editing ? (
          <div className="w-full space-y-2">
            <Textarea value={draft} onChange={(e) => setDraft(e.target.value)} rows={3} autoFocus />
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
                <X /> Cancel
              </Button>
              <Button
                size="sm"
                onClick={() => {
                  setEditing(false)
                  onEdit?.(draft)
                }}
              >
                <Check /> Save &amp; resend
              </Button>
            </div>
          </div>
        ) : (
          <div
            className={cn(
              "rounded-2xl px-4 py-2.5 text-sm",
              isUser ? "bg-primary text-primary-foreground" : "bg-muted",
            )}
          >
            {isUser ? (
              <p className="whitespace-pre-wrap">{message.content}</p>
            ) : (
              <MarkdownRenderer content={message.content || (streaming ? "" : "")} />
            )}
            {streaming && (
              <span className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-current align-text-bottom" />
            )}
          </div>
        )}

        {message.error && (
          <p className="text-xs text-destructive">{message.error}</p>
        )}

        {!!message.actions?.length && (
          <div className="flex flex-col gap-2">
            {message.actions.map((a) => (
              <ProposalCard key={a.id} action={a} />
            ))}
          </div>
        )}

        {!editing && !streaming && (
          <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
            <ActionIcon label="Copy" onClick={copy}>
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            </ActionIcon>
            {isUser && onEdit && (
              <ActionIcon label="Edit" onClick={() => setEditing(true)}>
                <Pencil className="size-3.5" />
              </ActionIcon>
            )}
            {!isUser && onRegenerate && (
              <ActionIcon label="Regenerate" onClick={onRegenerate}>
                <RotateCw className="size-3.5" />
              </ActionIcon>
            )}
            {onQuote && (
              <ActionIcon label="Quote" onClick={() => onQuote(message.content)}>
                <Quote className="size-3.5" />
              </ActionIcon>
            )}
            {onToggleBookmark && (
              <ActionIcon label={message.bookmarked ? "Remove bookmark" : "Bookmark"} onClick={onToggleBookmark}>
                <Bookmark className={cn("size-3.5", message.bookmarked && "fill-current")} />
              </ActionIcon>
            )}
            {!isUser && (
              <>
                <ActionIcon label="Good response" onClick={() => setReaction(reaction === "up" ? null : "up")}>
                  <ThumbsUp className={cn("size-3.5", reaction === "up" && "fill-current")} />
                </ActionIcon>
                <ActionIcon label="Bad response" onClick={() => setReaction(reaction === "down" ? null : "down")}>
                  <ThumbsDown className={cn("size-3.5", reaction === "down" && "fill-current")} />
                </ActionIcon>
              </>
            )}
            <ActionIcon label="Share (not available yet)" onClick={() => toast.info("Sharing isn't available yet")}>
              <Share2 className="size-3.5" />
            </ActionIcon>
            {onDelete && (
              <ActionIcon label="Delete" onClick={onDelete}>
                <Trash2 className="size-3.5" />
              </ActionIcon>
            )}
          </div>
        )}
      </div>
      {streaming && !message.content && (
        <div className="flex items-center gap-2 self-center text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" />
          Thinking…
        </div>
      )}
    </div>
  )
})

function ActionIcon({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon" className="size-6 text-muted-foreground hover:text-foreground" onClick={onClick} aria-label={label}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
