import { useEffect, useMemo, useState, type KeyboardEvent } from "react"
import { useSearchParams } from "react-router-dom"
import {
  Clock,
  FileUp,
  Grid3x3,
  List,
  Loader2,
  RotateCw,
  Search,
  Star,
  UploadCloud,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { HighlightText } from "@/components/highlight-text"
import { FileIcon } from "@/components/files/file-icon"
import { UploadDropzone, UploadButton } from "@/components/files/upload-dropzone"
import { UploadQueuePanel } from "@/components/files/upload-queue-panel"
import { FileDetailSheet } from "@/components/files/file-detail-sheet"
import { FilesBulkActionsBar } from "@/components/files/files-bulk-actions-bar"
import { useFiles, removeFilesFromStore } from "@/lib/files-store"
import { getFileMeta, markFileOpened, useFileMetaStore, recentlyOpenedFiles } from "@/lib/file-meta"
import { formatBytes, fileCategory } from "@/lib/file-render"
import { formatRelativeTime } from "@/lib/format"
import { useDebouncedValue } from "@/lib/use-debounced-value"
import { cn } from "@/lib/utils"
import { accentBorderClass } from "@/lib/collection-icons"
import type { FileRecord } from "@/lib/types"

type ViewMode = "grid" | "list"
type SortKey = "name" | "size" | "created"
type DateFilter = "any" | "today" | "week" | "month"
type SizeFilter = "any" | "small" | "medium" | "large"

const CATEGORIES = ["image", "pdf", "document", "spreadsheet", "text", "code", "video", "audio", "archive", "other"]

export function FilesPage() {
  const { files, total, nextCursor, loading, loadingMore, error, refresh, fetchMore } = useFiles()
  useFileMetaStore()

  const [view, setView] = useState<ViewMode>("grid")
  const [query, setQuery] = useState("")
  const debouncedQuery = useDebouncedValue(query, 250)
  const [category, setCategory] = useState<string>("any")
  const [dateFilter, setDateFilter] = useState<DateFilter>("any")
  const [sizeFilter, setSizeFilter] = useState<SizeFilter>("any")
  const [sortKey, setSortKey] = useState<SortKey>("created")
  const [favoritesOnly, setFavoritesOnly] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [openFile, setOpenFile] = useState<FileRecord | null>(null)
  const [searchParams, setSearchParams] = useSearchParams()
  const autoOpenUpload = searchParams.get("action") === "upload"

  // Lets the command palette's "Upload File" action pop the picker
  // immediately after navigating here.
  useEffect(() => {
    if (autoOpenUpload) {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        next.delete("action")
        return next
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoOpenUpload])

  const filtered = useMemo(() => {
    if (!files) return null
    let list = files
    if (debouncedQuery.trim()) {
      const q = debouncedQuery.trim().toLowerCase()
      list = list.filter((f) => f.filename.toLowerCase().includes(q))
    }
    if (category !== "any") list = list.filter((f) => fileCategory(f.mime, f.filename) === category)
    if (favoritesOnly) list = list.filter((f) => getFileMeta(f.id).favorite)
    if (dateFilter !== "any") {
      const now = Date.now()
      const cutoff = { today: 86400e3, week: 7 * 86400e3, month: 30 * 86400e3 }[dateFilter]
      list = list.filter((f) => now - new Date(f.created).getTime() <= cutoff)
    }
    if (sizeFilter !== "any") {
      list = list.filter((f) => {
        if (sizeFilter === "small") return f.size < 1024 * 1024
        if (sizeFilter === "medium") return f.size >= 1024 * 1024 && f.size < 20 * 1024 * 1024
        return f.size >= 20 * 1024 * 1024
      })
    }
    return [...list].sort((a, b) => {
      if (sortKey === "name") return a.filename.localeCompare(b.filename)
      if (sortKey === "size") return b.size - a.size
      return new Date(b.created).getTime() - new Date(a.created).getTime()
    })
  }, [files, debouncedQuery, category, favoritesOnly, dateFilter, sizeFilter, sortKey])

  const recent = useMemo(() => {
    if (!files) return []
    const ids = recentlyOpenedFiles(files.map((f) => f.id))
    return ids.map((id) => files.find((f) => f.id === id)).filter((f): f is FileRecord => !!f)
  }, [files])

  function openDetail(f: FileRecord) {
    markFileOpened(f.id)
    setOpenFile(f)
  }

  return (
    <UploadDropzone>
      <div className="space-y-6">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative flex-1 min-w-48">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input placeholder="Search files…" className="h-9 pl-8" value={query} onChange={(e) => setQuery(e.target.value)} />
          </div>
          <Select value={category} onValueChange={setCategory}>
            <SelectTrigger className="h-9 w-36">
              <SelectValue placeholder="Type" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">All types</SelectItem>
              {CATEGORIES.map((c) => (
                <SelectItem key={c} value={c}>
                  {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={dateFilter} onValueChange={(v) => setDateFilter(v as DateFilter)}>
            <SelectTrigger className="h-9 w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Any time</SelectItem>
              <SelectItem value="today">Today</SelectItem>
              <SelectItem value="week">This week</SelectItem>
              <SelectItem value="month">This month</SelectItem>
            </SelectContent>
          </Select>
          <Select value={sizeFilter} onValueChange={(v) => setSizeFilter(v as SizeFilter)}>
            <SelectTrigger className="h-9 w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Any size</SelectItem>
              <SelectItem value="small">&lt; 1 MB</SelectItem>
              <SelectItem value="medium">1–20 MB</SelectItem>
              <SelectItem value="large">&gt; 20 MB</SelectItem>
            </SelectContent>
          </Select>
          <Select value={sortKey} onValueChange={(v) => setSortKey(v as SortKey)}>
            <SelectTrigger className="h-9 w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="created">Newest</SelectItem>
              <SelectItem value="name">Name</SelectItem>
              <SelectItem value="size">Size</SelectItem>
            </SelectContent>
          </Select>
          <Button variant={favoritesOnly ? "secondary" : "outline"} size="sm" className="h-9" onClick={() => setFavoritesOnly((v) => !v)}>
            <Star className={cn("size-3.5", favoritesOnly && "fill-current")} />
            Favorites
          </Button>
          <div className="flex rounded-md border p-0.5">
            <Button variant={view === "grid" ? "secondary" : "ghost"} size="icon" className="size-8" onClick={() => setView("grid")} aria-label="Grid view">
              <Grid3x3 className="size-3.5" />
            </Button>
            <Button variant={view === "list" ? "secondary" : "ghost"} size="icon" className="size-8" onClick={() => setView("list")} aria-label="List view">
              <List className="size-3.5" />
            </Button>
          </div>
          <Button variant="outline" size="icon" className="size-9" onClick={() => refresh()} title="Refresh" aria-label="Refresh">
            <RotateCw className="size-3.5" />
          </Button>
          <UploadButton autoOpen={autoOpenUpload}>
            {(open) => (
              <Button size="sm" className="ml-auto" onClick={open}>
                <FileUp />
                Upload
              </Button>
            )}
          </UploadButton>
        </div>

        {selected.size > 0 && (
          <FilesBulkActionsBar
            files={files ?? []}
            selectedIds={selected}
            onClear={() => setSelected(new Set())}
            onDeleted={(ids) => {
              removeFilesFromStore(ids)
              setSelected(new Set())
            }}
          />
        )}

        {recent.length > 0 && !query && !favoritesOnly && (
          <div className="space-y-2">
            <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <Clock className="size-3.5" />
              Recently opened
            </p>
            <div className="flex flex-wrap gap-2">
              {recent.map((f) => (
                <button
                  key={f.id}
                  onClick={() => openDetail(f)}
                  className="flex items-center gap-2 rounded-full border py-1 pl-1 pr-3 text-sm transition-colors hover:bg-accent"
                >
                  <FileIcon mime={f.mime} filename={f.filename} color={getFileMeta(f.id).color} className="size-6" iconClassName="size-3" />
                  <span className="max-w-40 truncate">{f.filename}</span>
                </button>
              ))}
            </div>
          </div>
        )}

        {error && (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <UploadCloud />
              </EmptyMedia>
              <EmptyTitle>Couldn't load files</EmptyTitle>
              <EmptyDescription>{error}</EmptyDescription>
            </EmptyHeader>
            <Button size="sm" onClick={() => refresh()}>
              <RotateCw />
              Retry
            </Button>
          </Empty>
        )}

        {!error && loading && (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className="h-28 w-full" />
            ))}
          </div>
        )}

        {!error && !loading && files !== null && files.length === 0 && (
          <UploadButton>
            {(open) => (
              <Empty className="cursor-pointer" onClick={open}>
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <UploadCloud />
                  </EmptyMedia>
                  <EmptyTitle>No files yet</EmptyTitle>
                  <EmptyDescription>Drag and drop files anywhere on this page, or click to browse.</EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Button
                    size="sm"
                    onClick={(e) => {
                      e.stopPropagation()
                      open()
                    }}
                  >
                    <FileUp />
                    Upload files
                  </Button>
                </EmptyContent>
              </Empty>
            )}
          </UploadButton>
        )}

        {!error && !loading && filtered && filtered.length === 0 && files && files.length > 0 && (
          <p className="py-12 text-center text-sm text-muted-foreground">No files match your filters.</p>
        )}

        {!error && filtered && filtered.length > 0 && (
          <>
            <p className="text-xs text-muted-foreground">
              {filtered.length} of {total} file{total === 1 ? "" : "s"}
            </p>
            {view === "grid" ? (
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                {filtered.map((f) => (
                  <FileCard key={f.id} file={f} query={debouncedQuery} onOpen={() => openDetail(f)} selected={selected.has(f.id)}
                    onToggleSelect={(checked) =>
                      setSelected((prev) => {
                        const next = new Set(prev)
                        checked ? next.add(f.id) : next.delete(f.id)
                        return next
                      })
                    }
                  />
                ))}
              </div>
            ) : (
              <div className="divide-y rounded-lg border">
                {filtered.map((f) => (
                  <FileRow key={f.id} file={f} query={debouncedQuery} onOpen={() => openDetail(f)} selected={selected.has(f.id)}
                    onToggleSelect={(checked) =>
                      setSelected((prev) => {
                        const next = new Set(prev)
                        checked ? next.add(f.id) : next.delete(f.id)
                        return next
                      })
                    }
                  />
                ))}
              </div>
            )}
            {nextCursor && (
              <div className="flex justify-center">
                <Button variant="outline" size="sm" onClick={() => fetchMore()} disabled={loadingMore}>
                  {loadingMore && <Loader2 className="animate-spin" />}
                  Load more
                </Button>
              </div>
            )}
          </>
        )}
      </div>

      <FileDetailSheet
        file={openFile}
        onOpenChange={(open) => !open && setOpenFile(null)}
        onDeleted={(id) => {
          removeFilesFromStore([id])
          setOpenFile(null)
        }}
      />
      <UploadQueuePanel />
    </UploadDropzone>
  )
}

// Both FileCard and FileRow open a file from a plain onClick div — with no
// tabIndex/role/keyboard handler, a keyboard-only user had no way to reach
// or open a file at all (the selection checkbox was the only focusable
// element in each entry). This makes the whole row/card a real,
// keyboard-operable button while leaving its visual layout untouched.
function openOnEnterOrSpace(onOpen: () => void) {
  return (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget) return // let the checkbox handle its own Space/Enter
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
}

function FileCard({
  file,
  query,
  onOpen,
  selected,
  onToggleSelect,
}: {
  file: FileRecord
  query?: string
  onOpen: () => void
  selected: boolean
  onToggleSelect: (checked: boolean) => void
}) {
  const meta = getFileMeta(file.id)
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={`Open ${file.filename}`}
      className={cn(
        "group relative cursor-pointer rounded-lg border border-l-4 p-4 transition-shadow hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring",
        accentBorderClass(meta.color),
        selected && "ring-2 ring-primary",
      )}
      onClick={onOpen}
      onKeyDown={openOnEnterOrSpace(onOpen)}
    >
      <input
        type="checkbox"
        checked={selected}
        onChange={(e) => onToggleSelect(e.target.checked)}
        onClick={(e) => e.stopPropagation()}
        className="absolute left-3 top-3 opacity-0 group-hover:opacity-100 checked:opacity-100 focus-visible:opacity-100"
        aria-label={`Select ${file.filename}`}
      />
      <div className="flex flex-col items-center gap-2 text-center">
        {/* Icon color reflects file type only (not the organizational
            color the admin picked) — the card's left border above is
            the color accent, so the icon itself stays a reliable "what
            kind of file is this" signal. */}
        <FileIcon mime={file.mime} filename={file.filename} className="size-12" iconClassName="size-6" />
        <p className="line-clamp-2 w-full text-sm font-medium">
          <HighlightText text={file.filename} query={query} />
        </p>
        <p className="text-xs text-muted-foreground">{formatBytes(file.size)}</p>
      </div>
      {meta.favorite && <Star className="absolute right-3 top-3 size-3.5 fill-amber-400 text-amber-400" />}
    </div>
  )
}

function FileRow({
  file,
  query,
  onOpen,
  selected,
  onToggleSelect,
}: {
  file: FileRecord
  query?: string
  onOpen: () => void
  selected: boolean
  onToggleSelect: (checked: boolean) => void
}) {
  const meta = getFileMeta(file.id)
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={`Open ${file.filename}`}
      className={cn(
        "flex cursor-pointer items-center gap-3 border-l-4 px-4 py-2.5 text-sm hover:bg-accent focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
        accentBorderClass(meta.color),
        selected && "bg-accent/60",
      )}
      onClick={onOpen}
      onKeyDown={openOnEnterOrSpace(onOpen)}
    >
      <input
        type="checkbox"
        checked={selected}
        onChange={(e) => onToggleSelect(e.target.checked)}
        onClick={(e) => e.stopPropagation()}
        aria-label={`Select ${file.filename}`}
      />
      <FileIcon mime={file.mime} filename={file.filename} className="size-8" iconClassName="size-4" />
      <span className="min-w-0 flex-1 truncate font-medium">
        <HighlightText text={file.filename} query={query} />
      </span>
      {meta.tags.slice(0, 2).map((t) => (
        <Badge key={t} variant="secondary" className="hidden font-normal sm:inline-flex">
          {t}
        </Badge>
      ))}
      <span className="w-20 text-right text-xs text-muted-foreground">{formatBytes(file.size)}</span>
      <span className="hidden w-24 text-right text-xs text-muted-foreground md:inline">{formatRelativeTime(file.created)}</span>
      {meta.favorite && <Star className="size-3.5 fill-amber-400 text-amber-400" />}
    </div>
  )
}
