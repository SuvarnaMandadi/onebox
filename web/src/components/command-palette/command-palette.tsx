import { useEffect, useMemo, useRef, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { useTheme } from "next-themes"
import { toast } from "sonner"
import {
  Clock,
  Database,
  FileText,
  FolderOpen,
  LayoutDashboard,
  MessageSquare,
  Moon,
  Palette,
  Pin,
  Plus,
  RotateCw,
  Search,
  Settings,
  Sparkles,
  Star,
  Trash2,
  Upload,
} from "lucide-react"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "@/components/ui/command"
import { Badge } from "@/components/ui/badge"
import { HighlightText } from "@/components/highlight-text"
import { useCollections } from "@/lib/collections-store"
import { useFiles, clearFilesCache } from "@/lib/files-store"
import { clearCollectionsCache } from "@/lib/collections-store"
import { getMeta, recentlyOpened } from "@/lib/collection-meta"
import { getFileMeta, recentlyOpenedFiles } from "@/lib/file-meta"
import { useConversations } from "@/lib/ai-conversations"
import { PROMPT_LIBRARY } from "@/lib/prompt-library"
import { recordSearch, togglePinnedSearch, useSearchHistory } from "@/lib/search-history"
import { toggleCommandPalette, useCommandPaletteOpen } from "@/lib/command-palette-state"
import { useDebouncedValue } from "@/lib/use-debounced-value"
import { api } from "@/lib/api"
import { formatRelativeTime } from "@/lib/format"
import type { RecordRow } from "@/lib/types"

const isMac = typeof navigator !== "undefined" && /Mac|iPhone/.test(navigator.platform)

export function CommandPalette() {
  const [open, setOpen] = useCommandPaletteOpen()
  const [query, setQuery] = useState("")
  const debouncedQuery = useDebouncedValue(query, 150)
  const navigate = useNavigate()
  const location = useLocation()
  const { resolvedTheme, setTheme } = useTheme()

  const { collections, refresh: refreshCollections } = useCollections()
  const { files, refresh: refreshFiles } = useFiles()
  const conversations = useConversations()
  const searchHistory = useSearchHistory()

  const [contextRecords, setContextRecords] = useState<RecordRow[]>([])
  const fetchedForCollection = useRef<string | null>(null)

  // A collection is "open" when the route is /collections/:name(/records) —
  // matches Milestone 4's Context Awareness requirement without inventing
  // a global record index the backend can't support (no full-text search
  // API exists — see record_handlers.go's filter=field=value-only query).
  const contextCollection = useMemo(() => {
    const m = location.pathname.match(/^\/collections\/([^/]+)/)
    return m ? decodeURIComponent(m[1]) : null
  }, [location.pathname])

  // Prefetches as soon as a collection route is entered — deliberately
  // NOT gated on the palette being open. Two reasons: (1) it means the
  // Records group is already populated by the time Ctrl+K is pressed,
  // so cmdk registers those items while building its very first filtered
  // snapshot instead of registering them late into an already-active
  // search (cmdk's fuzzy-filter state only recomputes on registration/
  // search-change events, and items that join after a search is already
  // typed were observed not being picked up until the next keystroke —
  // prefetching sidesteps that entirely); (2) it's a nicer experience
  // regardless — no fetch-in-progress flicker on open. Still only one
  // request per collection (fetchedForCollection guards re-fetching on
  // every render), and it clears when navigating to a different (or no)
  // collection.
  useEffect(() => {
    if (!contextCollection) {
      setContextRecords([])
      fetchedForCollection.current = null
      return
    }
    if (fetchedForCollection.current === contextCollection) return
    fetchedForCollection.current = contextCollection
    api
      .get<{ items: RecordRow[] }>(`/api/collections/${contextCollection}/records?limit=50`)
      .then((res) => setContextRecords(res.items))
      .catch(() => setContextRecords([]))
  }, [contextCollection])

  useEffect(() => {
    if (!open) setQuery("")
  }, [open])

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        toggleCommandPalette()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [])

  function go(path: string, searchedQuery?: string) {
    if (searchedQuery) recordSearch(searchedQuery)
    navigate(path)
    setOpen(false)
  }

  function run(fn: () => void, searchedQuery?: string) {
    if (searchedQuery) recordSearch(searchedQuery)
    fn()
    setOpen(false)
  }

  const q = debouncedQuery.trim()

  // cmdk owns the actual text-matching (via each CommandItem's `value`,
  // see below) — these are just which candidate items get mounted at
  // all, capped so one huge collection/file list can't dominate the
  // palette. Passing meaningful `value`s (not opaque ids) is what makes
  // cmdk's own filtering, keyboard-selection, and Enter-to-select all
  // work correctly; an earlier version disabled cmdk's filter and
  // pre-filtered with useMemo instead, which broke keyboard selection
  // whenever the match set changed out from under it mid-keystroke.
  const candidateCollections = useMemo(() => (collections ?? []).slice(0, 25), [collections])
  const candidateFiles = useMemo(() => (files ?? []).slice(0, 25), [files])
  const candidateConversations = useMemo(() => conversations.slice(0, 25), [conversations])
  const candidateRecords = contextCollection ? contextRecords.slice(0, 25) : []
  // Records are a plain map[string]any (id/created/updated/owner_id
  // alongside the schema fields) — Object.values() has no notion of
  // which one a human would recognize the record by, and its iteration
  // order isn't the schema's field order either (JSON round-trips
  // alphabetically here), so picking "the first string value" landed on
  // `created` far more often than the field an admin actually typed.
  // Use the real schema (already in hand from collections-store) to pick
  // the first text field instead.
  const recordLabelField = useMemo(
    () => collections?.find((c) => c.name === contextCollection)?.schema.fields.find((f) => f.type === "text")?.name,
    [collections, contextCollection],
  )

  const favoriteCollections = (collections ?? []).filter((c) => getMeta(c.name).favorite)
  const favoriteFiles = (files ?? []).filter((f) => getFileMeta(f.id).favorite)
  const recentCollectionNames = recentlyOpened((collections ?? []).map((c) => c.name), 4)
  const recentFileIds = recentlyOpenedFiles((files ?? []).map((f) => f.id), 4)
  const recentConversations = [...conversations].sort((a, b) => b.updatedAt - a.updatedAt).slice(0, 4)
  const pinnedSearches = searchHistory.filter((h) => h.pinned)
  const recentSearches = searchHistory.filter((h) => !h.pinned).slice(0, 5)

  const commands = useMemo(
    () => [
      {
        id: "create-collection",
        label: "Create Collection",
        icon: Plus,
        keywords: "new add collection",
        run: () => go("/collections?action=new"),
      },
      {
        id: "create-record",
        label: "Create Record",
        icon: Plus,
        keywords: "new add record",
        run: () => (contextCollection ? go(`/collections/${contextCollection}/records?action=new`) : go("/collections")),
      },
      { id: "upload-file", label: "Upload File", icon: Upload, keywords: "new add file", run: () => go("/files?action=upload") },
      { id: "new-chat", label: "New Chat", icon: MessageSquare, keywords: "ai chat conversation", run: () => go("/ai?action=new") },
      { id: "open-settings", label: "Open Settings", icon: Settings, keywords: "preferences config", run: () => go("/settings") },
      {
        id: "toggle-theme",
        label: "Toggle Theme",
        icon: resolvedTheme === "dark" ? Moon : Palette,
        keywords: "dark light mode appearance",
        run: () => run(() => setTheme(resolvedTheme === "dark" ? "light" : "dark")),
      },
      { id: "open-ai", label: "Open AI Workspace", icon: Sparkles, keywords: "chat assistant copilot", run: () => go("/ai") },
      { id: "open-collections", label: "Open Collections", icon: Database, keywords: "tables schema", run: () => go("/collections") },
      { id: "open-files", label: "Open Files", icon: FolderOpen, keywords: "documents uploads", run: () => go("/files") },
      {
        id: "open-records",
        label: "Open Records",
        icon: LayoutDashboard,
        keywords: "rows data table",
        run: () => (contextCollection ? go(`/collections/${contextCollection}/records`) : go("/collections")),
      },
      {
        id: "refresh-data",
        label: "Refresh Data",
        icon: RotateCw,
        keywords: "reload sync",
        run: () =>
          run(() => {
            refreshCollections()
            refreshFiles()
            toast.success("Refreshing data…")
          }),
      },
      {
        id: "clear-cache",
        label: "Clear Cache",
        icon: Trash2,
        keywords: "reset cache purge",
        run: () =>
          run(() => {
            clearCollectionsCache()
            clearFilesCache()
            toast.success("Cache cleared — reloading")
          }),
      },
      {
        id: "focus-search",
        label: "Focus Search",
        icon: Search,
        keywords: "find search",
        run: () => setOpen(true),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [contextCollection, resolvedTheme, refreshCollections, refreshFiles],
  )

  return (
    <CommandDialog open={open} onOpenChange={setOpen} title="Command Center" description="Search collections, records, files, chats, and commands">
      <Command>
      <CommandInput placeholder="Search or run a command…" value={query} onValueChange={setQuery} />
      <CommandList className="max-h-[60vh]">
        <CommandEmpty>Nothing here yet.</CommandEmpty>

        {!q && (
          <>
            {(pinnedSearches.length > 0 || recentSearches.length > 0) && (
              <CommandGroup heading="Search history">
                {pinnedSearches.map((h) => (
                  <CommandItem key={h.id} value={`history-${h.id}`} onSelect={() => setQuery(h.query)}>
                    <Pin className="size-3.5" />
                    {h.query}
                    <button
                      type="button"
                      className="ml-auto text-muted-foreground hover:text-foreground"
                      onClick={(e) => {
                        e.stopPropagation()
                        togglePinnedSearch(h.id)
                      }}
                    >
                      Unpin
                    </button>
                  </CommandItem>
                ))}
                {recentSearches.map((h) => (
                  <CommandItem key={h.id} value={`history-${h.id}`} onSelect={() => setQuery(h.query)}>
                    <Clock className="size-3.5" />
                    {h.query}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {(favoriteCollections.length > 0 || favoriteFiles.length > 0) && (
              <CommandGroup heading="Favorites">
                {favoriteCollections.map((c) => (
                  <CommandItem key={c.id} value={`fav-col-${c.id}`} onSelect={() => go(`/collections/${c.name}`)}>
                    <Database className="size-3.5" /> {c.name}
                    <Star className="ml-auto size-3 fill-amber-400 text-amber-400" />
                  </CommandItem>
                ))}
                {favoriteFiles.map((f) => (
                  <CommandItem key={f.id} value={`fav-file-${f.id}`} onSelect={() => go("/files")}>
                    <FileText className="size-3.5" /> {f.filename}
                    <Star className="ml-auto size-3 fill-amber-400 text-amber-400" />
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {(recentCollectionNames.length > 0 || recentFileIds.length > 0 || recentConversations.length > 0) && (
              <CommandGroup heading="Recent activity">
                {recentCollectionNames.map((name) => (
                  <CommandItem key={name} value={`recent-col-${name}`} onSelect={() => go(`/collections/${name}`)}>
                    <Database className="size-3.5" /> {name}
                  </CommandItem>
                ))}
                {recentFileIds.map((id) => {
                  const f = files?.find((f) => f.id === id)
                  if (!f) return null
                  return (
                    <CommandItem key={id} value={`recent-file-${id}`} onSelect={() => go("/files")}>
                      <FileText className="size-3.5" /> {f.filename}
                    </CommandItem>
                  )
                })}
                {recentConversations.map((c) => (
                  <CommandItem key={c.id} value={`recent-chat-${c.id}`} onSelect={() => go(`/ai?conversation=${c.id}`)}>
                    <MessageSquare className="size-3.5" />
                    {c.title}
                    <span className="ml-auto text-xs text-muted-foreground">{formatRelativeTime(new Date(c.updatedAt).toISOString())}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            <CommandSeparator />
          </>
        )}

        {candidateCollections.length > 0 && (
          <CommandGroup heading="Collections">
            {candidateCollections.map((c) => (
              <CommandItem key={c.id} value={c.name} keywords={["collection"]} onSelect={() => go(`/collections/${c.name}`, query)}>
                <Database className="size-3.5" />
                <HighlightText text={c.name} query={query} />
                <span className="ml-auto text-xs text-muted-foreground">{c.record_count} records</span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {candidateRecords.length > 0 && (
          <CommandGroup heading={`Records in ${contextCollection}`}>
            {candidateRecords.map((r) => {
              const fieldValue = recordLabelField ? r[recordLabelField] : undefined
              const label = typeof fieldValue === "string" && fieldValue ? fieldValue : r.id
              return (
                <CommandItem
                  key={r.id}
                  value={`${label} ${r.id}`}
                  onSelect={() => go(`/collections/${contextCollection}/records`, query)}
                >
                  <LayoutDashboard className="size-3.5" />
                  <span className="truncate">{label}</span>
                </CommandItem>
              )
            })}
          </CommandGroup>
        )}

        {candidateFiles.length > 0 && (
          <CommandGroup heading="Files">
            {candidateFiles.map((f) => (
              <CommandItem key={f.id} value={f.filename} keywords={["file"]} onSelect={() => go("/files", query)}>
                <FileText className="size-3.5" />
                <HighlightText text={f.filename} query={query} />
                <Badge variant="outline" className="ml-auto font-normal">
                  {f.mime.split("/")[0]}
                </Badge>
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {candidateConversations.length > 0 && (
          <CommandGroup heading="Chats">
            {candidateConversations.map((c) => (
              <CommandItem key={c.id} value={c.title || "Untitled chat"} keywords={["chat", "conversation"]} onSelect={() => go(`/ai?conversation=${c.id}`, query)}>
                <MessageSquare className="size-3.5" />
                <HighlightText text={c.title || "Untitled chat"} query={query} />
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        <CommandGroup heading="Prompt templates">
          {PROMPT_LIBRARY.map((p) => (
            <CommandItem key={p.id} value={p.label} keywords={["prompt", "template"]} onSelect={() => go(`/ai?prefill=${encodeURIComponent(p.prompt)}`, query)}>
              <Sparkles className="size-3.5" />
              <HighlightText text={p.label} query={query} />
            </CommandItem>
          ))}
        </CommandGroup>

        <CommandGroup heading="Commands">
          {commands.map((c) => (
            <CommandItem key={c.id} value={c.label} keywords={c.keywords.split(" ")} onSelect={c.run}>
              <c.icon className="size-3.5" />
              <HighlightText text={c.label} query={query} />
            </CommandItem>
          ))}
        </CommandGroup>

        {q && (
          <CommandGroup heading="AI">
            <CommandItem value={`Ask AI ${q}`} onSelect={() => go(`/ai?prefill=${encodeURIComponent(query)}`, query)}>
              <Sparkles className="size-3.5" />
              Ask AI: "{query}"
            </CommandItem>
          </CommandGroup>
        )}
      </CommandList>
      <div className="flex items-center justify-between border-t px-3 py-1.5 text-[11px] text-muted-foreground">
        <span>↑↓ navigate · Enter select · Esc close</span>
        <CommandShortcut>{isMac ? "⌘K" : "Ctrl+K"}</CommandShortcut>
      </div>
      </Command>
    </CommandDialog>
  )
}
