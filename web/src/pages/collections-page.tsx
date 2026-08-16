import { useEffect, useMemo, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { ArrowUpDown, Clock, Database, LayoutGrid, List, Search, Star } from "lucide-react"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { CollectionIcon } from "@/components/collection-icon"
import { CreateCollectionDialog } from "@/components/create-collection-dialog"
import { useCollections } from "@/lib/collections-store"
import { getMeta, toggleFavorite, useCollectionMetaStore, recentlyOpened } from "@/lib/collection-meta"
import { formatRelativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Collection } from "@/lib/types"

type SortKey = "name" | "records" | "updated"
type ViewMode = "grid" | "list"

export function CollectionsPage() {
  const { collections, error } = useCollections()
  useCollectionMetaStore() // re-render when favorites/icons change
  const [query, setQuery] = useState("")
  const [sortKey, setSortKey] = useState<SortKey>("name")
  const [favoritesOnly, setFavoritesOnly] = useState(false)
  const [view, setView] = useState<ViewMode>("grid")
  const [createOpen, setCreateOpen] = useState(false)
  const [searchParams, setSearchParams] = useSearchParams()

  // Lets the command palette's "Create Collection" action open this
  // dialog immediately after navigating here, instead of landing on the
  // page and requiring a second click.
  useEffect(() => {
    if (searchParams.get("action") === "new") {
      setCreateOpen(true)
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        next.delete("action")
        return next
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams])

  const filtered = useMemo(() => {
    if (!collections) return null
    let list = collections
    if (query.trim()) {
      const q = query.trim().toLowerCase()
      list = list.filter(
        (c) => c.name.toLowerCase().includes(q) || getMeta(c.name).description.toLowerCase().includes(q),
      )
    }
    if (favoritesOnly) list = list.filter((c) => getMeta(c.name).favorite)
    return [...list].sort((a, b) => {
      if (sortKey === "records") return b.record_count - a.record_count
      if (sortKey === "updated") return new Date(b.updated).getTime() - new Date(a.updated).getTime()
      return a.name.localeCompare(b.name)
    })
  }, [collections, query, favoritesOnly, sortKey])

  const recent = useMemo(() => {
    if (!collections) return []
    const names = recentlyOpened(collections.map((c) => c.name))
    return names
      .map((n) => collections.find((c) => c.name === n))
      .filter((c): c is Collection => !!c)
  }, [collections])

  if (error) return <p className="text-sm text-destructive">{error}</p>

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-48">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder="Search collections…"
            className="h-9 pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <Select value={sortKey} onValueChange={(v) => setSortKey(v as SortKey)}>
          <SelectTrigger className="h-9 w-40">
            <ArrowUpDown className="size-3.5 text-muted-foreground" />
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="name">Name</SelectItem>
            <SelectItem value="records">Record count</SelectItem>
            <SelectItem value="updated">Last modified</SelectItem>
          </SelectContent>
        </Select>
        <Button
          variant={favoritesOnly ? "secondary" : "outline"}
          size="sm"
          className="h-9"
          onClick={() => setFavoritesOnly((v) => !v)}
        >
          <Star className={cn("size-3.5", favoritesOnly && "fill-current")} />
          Favorites
        </Button>
        <div className="flex rounded-md border p-0.5">
          <Button
            variant={view === "grid" ? "secondary" : "ghost"}
            size="icon"
            className="size-8"
            onClick={() => setView("grid")}
            aria-label="Grid view"
          >
            <LayoutGrid className="size-3.5" />
          </Button>
          <Button
            variant={view === "list" ? "secondary" : "ghost"}
            size="icon"
            className="size-8"
            onClick={() => setView("list")}
            aria-label="List view"
          >
            <List className="size-3.5" />
          </Button>
        </div>
        <CreateCollectionDialog open={createOpen} onOpenChange={setCreateOpen} />
      </div>

      {recent.length > 0 && !query && !favoritesOnly && (
        <div className="space-y-2">
          <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <Clock className="size-3.5" />
            Recently opened
          </p>
          <div className="flex flex-wrap gap-2">
            {recent.map((c) => {
              const meta = getMeta(c.name)
              return (
                <Link
                  key={c.id}
                  to={`/collections/${c.name}`}
                  className="flex items-center gap-2 rounded-full border py-1 pl-1 pr-3 text-sm transition-colors hover:bg-accent"
                >
                  <CollectionIcon icon={meta.icon} color={meta.color} className="size-6" iconClassName="size-3" />
                  {c.name}
                </Link>
              )
            })}
          </div>
        </div>
      )}

      {collections === null && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-36 w-full" />
          ))}
        </div>
      )}

      {collections !== null && collections.length === 0 && (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Database />
            </EmptyMedia>
            <EmptyTitle>No collections yet</EmptyTitle>
            <EmptyDescription>
              Collections are schema-defined tables with a REST API and realtime subscriptions built in.
              Ask the AI copilot to design one, or create it yourself.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <div className="flex justify-center gap-2">
              <CreateCollectionDialog />
              <Button asChild size="sm" variant="outline">
                <Link to="/ai">Ask the copilot</Link>
              </Button>
            </div>
          </EmptyContent>
        </Empty>
      )}

      {filtered && filtered.length === 0 && collections && collections.length > 0 && (
        <p className="py-12 text-center text-sm text-muted-foreground">No collections match your filters.</p>
      )}

      {filtered && filtered.length > 0 && view === "grid" && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {filtered.map((c) => (
            <CollectionCard key={c.id} collection={c} />
          ))}
        </div>
      )}

      {filtered && filtered.length > 0 && view === "list" && (
        <div className="divide-y rounded-lg border">
          {filtered.map((c) => (
            <CollectionRow key={c.id} collection={c} />
          ))}
        </div>
      )}
    </div>
  )
}

function FavoriteButton({ name, favorite }: { name: string; favorite: boolean }) {
  return (
    <button
      type="button"
      aria-label={favorite ? "Unfavorite" : "Favorite"}
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        toggleFavorite(name)
      }}
      className="text-muted-foreground transition-colors hover:text-foreground"
    >
      <Star className={cn("size-4", favorite && "fill-amber-400 text-amber-400")} />
    </button>
  )
}

function CollectionCard({ collection: c }: { collection: Collection }) {
  const meta = getMeta(c.name)
  return (
    <Link to={`/collections/${c.name}`}>
      <Card className="h-full transition-shadow hover:shadow-md">
        <CardHeader className="flex-row items-start justify-between space-y-0">
          <div className="flex items-center gap-3">
            <CollectionIcon icon={meta.icon} color={meta.color} />
            <div>
              <p className="font-medium leading-none">{c.name}</p>
              {meta.description && (
                <p className="mt-1 line-clamp-1 text-xs text-muted-foreground">{meta.description}</p>
              )}
            </div>
          </div>
          <FavoriteButton name={c.name} favorite={meta.favorite} />
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap gap-1">
            {c.schema.fields.slice(0, 4).map((f) => (
              <Badge key={f.name} variant="secondary" className="font-normal">
                {f.name}
              </Badge>
            ))}
            {c.schema.fields.length === 0 && (
              <span className="text-xs text-muted-foreground">No fields yet</span>
            )}
            {c.schema.fields.length > 4 && (
              <Badge variant="outline" className="font-normal">
                +{c.schema.fields.length - 4} more
              </Badge>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            {c.record_count} record{c.record_count === 1 ? "" : "s"} · updated {formatRelativeTime(c.updated)}
          </p>
        </CardContent>
      </Card>
    </Link>
  )
}

function CollectionRow({ collection: c }: { collection: Collection }) {
  const meta = getMeta(c.name)
  return (
    <Link
      to={`/collections/${c.name}`}
      className="flex items-center gap-3 px-4 py-3 text-sm transition-colors hover:bg-accent"
    >
      <CollectionIcon icon={meta.icon} color={meta.color} className="size-8" iconClassName="size-4" />
      <div className="min-w-0 flex-1">
        <p className="font-medium">{c.name}</p>
        {meta.description && <p className="truncate text-xs text-muted-foreground">{meta.description}</p>}
      </div>
      <span className="hidden text-xs text-muted-foreground sm:inline">{c.schema.fields.length} fields</span>
      <span className="w-20 text-right text-xs text-muted-foreground">{c.record_count} records</span>
      <span className="hidden w-24 text-right text-xs text-muted-foreground md:inline">
        {formatRelativeTime(c.updated)}
      </span>
      <FavoriteButton name={c.name} favorite={meta.favorite} />
    </Link>
  )
}
