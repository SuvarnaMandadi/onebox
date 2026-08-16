import { useEffect, useState } from "react"
import { Brain, ChevronDown, Cpu, Database, FileText, Gauge, MessageSquare } from "lucide-react"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { api } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useCollections } from "@/lib/collections-store"
import { useFiles } from "@/lib/files-store"
import { frequentlyUsed, useCollectionMetaStore } from "@/lib/collection-meta"
import { frequentlyUsedFiles, useFileMetaStore } from "@/lib/file-meta"
import type { AiConversation } from "@/lib/ai-conversations"

export function ContextPanel({ conversation }: { conversation: AiConversation | undefined }) {
  const [model, setModel] = useState<{ provider: string; model: string } | null>(null)
  const { collections: allCollections } = useCollections()
  const { files: allFiles } = useFiles()
  useCollectionMetaStore()
  useFileMetaStore()

  useEffect(() => {
    api
      .get<Record<string, string>>("/api/settings")
      .then((s) => setModel({ provider: s.chat_provider || "—", model: s.chat_model || "not chosen" }))
      .catch(() => setModel(null))
  }, [])

  const messages = conversation?.messages ?? []
  const totalChars = messages.reduce((sum, m) => sum + m.content.length, 0)
  // Same rough heuristic answerChatbotQuestion itself logs server-side
  // (~4 chars/token, English text) — labeled as an estimate, not a
  // provider-reported count, since the API never returns one for the
  // whole conversation.
  const estimatedTokens = Math.round(totalChars / 4)

  const collections = uniq(messages.flatMap((m) => m.contextRefs?.filter((r) => r.type === "collection").map((r) => r.collection) ?? []))
  const records = uniq(
    messages.flatMap((m) => m.contextRefs?.filter((r) => r.type === "record").map((r) => `${r.collection}#${r.record_id?.slice(0, 8)}`) ?? []),
  )
  const files = uniq(messages.flatMap((m) => m.attachments?.map((a) => a.filename) ?? []))

  // Workspace memory: which collections/files this browser has opened
  // most often, per collection-meta.ts/file-meta.ts's openCount tracking.
  // Local/per-browser only — see the "About this panel" note below.
  const frequentCollections = frequentlyUsed((allCollections ?? []).map((c) => c.name))
  const frequentFiles = frequentlyUsedFiles((allFiles ?? []).map((f) => f.id))
    .map((id) => allFiles?.find((f) => f.id === id)?.filename)
    .filter((name): name is string => !!name)

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-3 text-sm">
      <div>
        <p className="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <Cpu className="size-3.5" /> Model
        </p>
        <div className="rounded-md border p-2 text-xs">
          <p className="font-medium capitalize">{model?.provider ?? "Loading…"}</p>
          {model?.model === "not chosen" ? (
            <a href="/_/#/settings" className="text-primary underline underline-offset-2">
              not chosen — configure a provider
            </a>
          ) : (
            <p className="text-muted-foreground">{model?.model}</p>
          )}
        </div>
      </div>

      <Separator />

      <div>
        <p className="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <Gauge className="size-3.5" /> Conversation size
        </p>
        <div className="grid grid-cols-2 gap-2 text-xs">
          <Stat label="Messages" value={messages.length} />
          <Stat label="Est. tokens" value={estimatedTokens.toLocaleString()} />
        </div>
      </div>

      <Separator />

      <PanelSection icon={Database} title="Mentioned collections" items={collections} empty="None mentioned yet — use @ in the composer." />
      <PanelSection icon={Database} title="Mentioned records" items={records} empty="None mentioned yet." />
      <PanelSection icon={FileText} title="Attached files" items={files} empty="No files attached in this conversation." />

      <Separator />

      <PanelSection
        icon={Brain}
        title="Frequently used"
        items={[...frequentCollections, ...frequentFiles]}
        empty="Nothing tracked yet — this fills in as you open collections and files."
      />

      <Separator />
      <div>
        <p className="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <MessageSquare className="size-3.5" /> About this panel
        </p>
        <p className="text-xs text-muted-foreground">
          Context here reflects what's actually been sent to the model in this conversation — mentioning a
          collection or attaching a file injects its real, live data (see the Collections/Files workspaces),
          nothing here is simulated. "Frequently used" is the one exception: it's tracked locally in this
          browser's storage (how often you've opened each collection/file), not read from the server, and
          isn't sent to the model.
        </p>
      </div>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-md border p-2">
      <p className="text-muted-foreground">{label}</p>
      <p className="font-medium">{value}</p>
    </div>
  )
}

function PanelSection({ icon: Icon, title, items, empty }: { icon: typeof Database; title: string; items: string[]; empty: string }) {
  return (
    <Collapsible defaultOpen={items.length > 0}>
      <CollapsibleTrigger className="group flex w-full items-center justify-between text-xs font-medium text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <Icon className="size-3.5" /> {title} {items.length > 0 && `(${items.length})`}
        </span>
        <ChevronDown className="size-3.5 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent className="mt-2">
        {items.length === 0 ? (
          <p className="text-xs text-muted-foreground">{empty}</p>
        ) : (
          <div className="flex flex-wrap gap-1">
            {items.map((it) => (
              <Badge key={it} variant="secondary" className={cn("font-normal")}>
                {it}
              </Badge>
            ))}
          </div>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}

function uniq(arr: string[]): string[] {
  return Array.from(new Set(arr))
}
