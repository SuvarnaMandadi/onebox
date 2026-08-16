import { useEffect, useState } from "react"
import { Link, Navigate, useParams } from "react-router-dom"
import { toast } from "sonner"
import { ArrowLeft, Copy, Loader2, Pencil, Sparkles, Star, Table2, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent } from "@/components/ui/card"
import { Textarea } from "@/components/ui/textarea"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Empty, EmptyDescription, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { CollectionIcon } from "@/components/collection-icon"
import { EditCollectionDialog } from "@/components/edit-collection-dialog"
import { DeleteCollectionDialog } from "@/components/delete-collection-dialog"
import { useCollections } from "@/lib/collections-store"
import { getMeta, markOpened, toggleFavorite, useCollectionMetaStore } from "@/lib/collection-meta"
import { formatRelativeTime } from "@/lib/format"
import { api, ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ChatbotResponse, Field, LogEntry } from "@/lib/types"

export function CollectionDetailPage() {
  const { name = "" } = useParams()
  const { collections, error } = useCollections()
  useCollectionMetaStore()

  useEffect(() => {
    markOpened(name)
  }, [name])

  if (error) return <p className="text-sm text-destructive">{error}</p>

  if (collections === null) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    )
  }

  const collection = collections.find((c) => c.name === name)
  if (!collection) return <Navigate to="/collections" replace />

  const meta = getMeta(name)

  return (
    <div className="space-y-6">
      <Button asChild variant="ghost" size="sm" className="-ml-2 text-muted-foreground">
        <Link to="/collections">
          <ArrowLeft />
          Collections
        </Link>
      </Button>

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <CollectionIcon icon={meta.icon} color={meta.color} className="size-11" iconClassName="size-5" />
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-xl font-semibold tracking-tight">{collection.name}</h2>
              <button
                type="button"
                aria-label={meta.favorite ? "Unfavorite" : "Favorite"}
                onClick={() => toggleFavorite(name)}
                className="text-muted-foreground hover:text-foreground"
              >
                <Star className={cn("size-4", meta.favorite && "fill-amber-400 text-amber-400")} />
              </button>
            </div>
            <p className="text-sm text-muted-foreground">
              {meta.description || "No description yet."}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button asChild size="sm">
            <Link to={`/collections/${name}/records`}>
              <Table2 />
              Open records
            </Link>
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(`${location.origin}/api/collections/${name}/records`)
              toast.success("API endpoint copied")
            }}
          >
            <Copy />
            Copy API URL
          </Button>
          <EditCollectionDialog collection={collection}>
            <Button variant="outline" size="sm">
              <Pencil />
              Edit
            </Button>
          </EditCollectionDialog>
          <DeleteCollectionDialog collectionName={name}>
            <Button variant="outline" size="sm" className="text-destructive hover:text-destructive">
              <Trash2 />
              Delete
            </Button>
          </DeleteCollectionDialog>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Fields" value={collection.schema.fields.length} />
        <Stat label="Records" value={collection.record_count} />
        <Stat label="Created" value={formatRelativeTime(collection.created)} />
        <Stat label="Updated" value={formatRelativeTime(collection.updated)} />
      </div>

      <Tabs defaultValue="schema">
        <TabsList>
          <TabsTrigger value="schema">Schema</TabsTrigger>
          <TabsTrigger value="activity">Activity</TabsTrigger>
          <TabsTrigger value="ai">
            <Sparkles className="size-3.5" />
            Ask AI
          </TabsTrigger>
        </TabsList>
        <TabsContent value="schema" className="mt-4">
          <SchemaTab fields={collection.schema.fields} />
        </TabsContent>
        <TabsContent value="activity" className="mt-4">
          <ActivityTab collectionName={name} />
        </TabsContent>
        <TabsContent value="ai" className="mt-4">
          <AiTab collectionName={name} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="text-lg font-semibold leading-none">{value}</p>
      </CardContent>
    </Card>
  )
}

function SchemaTab({ fields }: { fields: Field[] }) {
  const autoColumns = ["id", "created", "updated", "owner_id"]
  return (
    <Card>
      <CardContent className="p-0">
        <div className="divide-y">
          {fields.map((f) => (
            <div key={f.name} className="flex items-center justify-between px-4 py-2.5 text-sm">
              <span className="font-medium">
                {f.name}
                {f.type === "relation" && f.relation_collection && (
                  <Link
                    to={`/collections/${f.relation_collection}`}
                    className="ml-1.5 text-xs font-normal text-muted-foreground hover:text-foreground hover:underline"
                  >
                    -&gt; {f.relation_collection}
                  </Link>
                )}
              </span>
              <div className="flex items-center gap-2">
                {f.required && (
                  <Badge variant="outline" className="font-normal">
                    required
                  </Badge>
                )}
                <Badge variant="secondary" className="font-normal">
                  {f.type}
                </Badge>
              </div>
            </div>
          ))}
          {autoColumns.map((name) => (
            <div key={name} className="flex items-center justify-between px-4 py-2.5 text-sm text-muted-foreground">
              <span>{name}</span>
              <Badge variant="outline" className="font-normal">
                auto
              </Badge>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

function ActivityTab({ collectionName }: { collectionName: string }) {
  const [logs, setLogs] = useState<LogEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api
      .get<{ items: LogEntry[] }>(`/api/logs?path=${encodeURIComponent(`/collections/${collectionName}`)}`)
      .then((res) => setLogs(res.items))
      .catch((err) => setError(err instanceof ApiError ? err.message : "Failed to load activity."))
  }, [collectionName])

  if (error) return <p className="text-sm text-destructive">{error}</p>
  if (logs === null) return <Skeleton className="h-40 w-full" />

  if (logs.length === 0) {
    return (
      <Empty>
        <EmptyMedia variant="icon">
          <Sparkles />
        </EmptyMedia>
        <EmptyTitle>No activity yet</EmptyTitle>
        <EmptyDescription>API requests to this collection will show up here.</EmptyDescription>
      </Empty>
    )
  }

  return (
    <Card>
      <CardContent className="p-0">
        <div className="divide-y">
          {logs.slice(0, 20).map((log) => (
            <div key={log.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <Badge variant={log.status < 400 ? "secondary" : "destructive"} className="w-14 justify-center font-mono font-normal">
                {log.method}
              </Badge>
              <span className="min-w-0 flex-1 truncate text-muted-foreground">{log.path}</span>
              <span className="text-xs text-muted-foreground">{log.status}</span>
              <span className="w-20 text-right text-xs text-muted-foreground">{formatRelativeTime(log.time)}</span>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

const COLLECTION_SUGGESTIONS = [
  { label: "Improve schema", prompt: "Review this collection's schema and suggest concrete improvements." },
  { label: "Find unused fields", prompt: "Look at this collection's fields and flag any that seem unused, redundant, or poorly named." },
  { label: "Normalize names", prompt: "Check this collection's field names for consistent naming conventions and suggest fixes." },
]

function AiTab({ collectionName }: { collectionName: string }) {
  const { refresh } = useCollections()
  const [message, setMessage] = useState("Summarize this collection and suggest improvements.")
  const [reply, setReply] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function ask(text: string) {
    setLoading(true)
    setError(null)
    setReply(null)
    try {
      const res = await api.post<ChatbotResponse>("/api/chat", {
        message: text,
        context: { page: "collections", collection: collectionName },
      })
      setReply(res.reply)
      if (res.actions?.length) {
        toast.info(`AI made ${res.actions.length} change(s) — refreshing`)
        await refresh()
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to reach the AI copilot.")
    } finally {
      setLoading(false)
    }
  }

  return (
    <Card>
      <CardContent className="space-y-3 p-4">
        <p className="text-xs text-muted-foreground">
          Scoped to <span className="font-medium">{collectionName}</span> — the copilot sees this
          collection's live schema. Safe changes (like adding a field) run for real; destructive ones are
          proposed for confirmation from the classic dashboard.
        </p>
        <div className="flex flex-wrap gap-1.5">
          {COLLECTION_SUGGESTIONS.map((s) => (
            <Button key={s.label} size="sm" variant="outline" disabled={loading} onClick={() => { setMessage(s.prompt); ask(s.prompt) }}>
              {s.label}
            </Button>
          ))}
        </div>
        <Textarea value={message} onChange={(e) => setMessage(e.target.value)} rows={2} />
        <Button size="sm" onClick={() => ask(message)} disabled={loading || !message.trim()}>
          {loading ? <Loader2 className="animate-spin" /> : <Sparkles />}
          Ask
        </Button>
        {error && <p className="text-sm text-destructive">{error}</p>}
        {reply && <p className="whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-sm">{reply}</p>}
      </CardContent>
    </Card>
  )
}
