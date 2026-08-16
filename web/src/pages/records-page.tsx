import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Link, Navigate, useParams, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import {
  AlertTriangle,
  ArrowLeft,
  Columns3,
  Database,
  Filter as FilterIcon,
  Loader2,
  Maximize2,
  Minimize2,
  RotateCw,
} from "lucide-react"
import type { SortingState, VisibilityState } from "@tanstack/react-table"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Label } from "@/components/ui/label"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { CollectionIcon } from "@/components/collection-icon"
import { RecordsTable, type Density } from "@/components/records/records-table"
import { CreateRecordSheet } from "@/components/records/create-record-sheet"
import { RecordDetailSheet } from "@/components/records/record-detail-sheet"
import { BulkActionsBar } from "@/components/records/bulk-actions-bar"
import { useCollections } from "@/lib/collections-store"
import { getMeta } from "@/lib/collection-meta"
import { useDebouncedValue } from "@/lib/use-debounced-value"
import { useRelationLabels } from "@/lib/relations"
import { api, ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { RecordRow, RecordsListResponse } from "@/lib/types"

export function RecordsPage() {
  const { name = "" } = useParams()
  const { collections } = useCollections()
  const collection = collections?.find((c) => c.name === name)

  const [rows, setRows] = useState<RecordRow[] | null>(null)
  const [nextCursor, setNextCursor] = useState<string | undefined>()
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [search, setSearch] = useState("")
  const debouncedSearch = useDebouncedValue(search, 250)
  const [columnFilters, setColumnFilters] = useState<Record<string, string>>({})
  const [appliedFilters, setAppliedFilters] = useState<Record<string, string>>({})

  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  const [sorting, setSorting] = useState<SortingState>([])
  const [density, setDensity] = useState<Density>("comfortable")
  const [fullscreen, setFullscreen] = useState(false)
  const [openRecord, setOpenRecord] = useState<RecordRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [searchParams, setSearchParams] = useSearchParams()

  // Lets the command palette's "Create Record" action (context-aware, or
  // picked via its collection sub-list) open this sheet immediately after
  // navigating here.
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

  // Deep-links a specific record open — the target of a relation field's
  // link (see FieldCell's "relation" case, records/field-cell.tsx), same
  // "?action=new" query-param convention as the create-sheet trigger above.
  // The linked record might not be among the (paginated, limit=200) rows
  // already loaded, so this fetches it directly by id when needed rather
  // than assuming it's in `rows`.
  useEffect(() => {
    const openID = searchParams.get("open")
    if (!openID || !name) return
    const inLoaded = rows?.find((r) => r.id === openID)
    if (inLoaded) {
      setOpenRecord(inLoaded)
    } else {
      api
        .get<RecordRow>(`/api/collections/${name}/records/${openID}`)
        .then(setOpenRecord)
        .catch(() => toast.error("That related record couldn't be found — it may have been deleted."))
    }
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete("open")
      return next
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams, name])

  const fields = useMemo(() => collection?.schema.fields ?? [], [collection])
  const relationLabels = useRelationLabels(fields, collections)

  // Tracks the collection this page is *currently* showing, kept in sync
  // every render (not via an effect, so it's never a render behind). load()
  // below checks this after each await: if the admin has already navigated
  // to a different collection by the time a request resolves, the stale
  // response is discarded instead of overwriting the newly-selected
  // collection's rows with data for the one they left.
  const currentNameRef = useRef(name)
  currentNameRef.current = name

  async function load(cursor?: string, filters: Record<string, string> = appliedFilters) {
    if (!name) return
    const requestName = name
    cursor ? setLoadingMore(true) : setLoading(true)
    setError(null)
    try {
      const params = new URLSearchParams({ limit: "200" })
      if (cursor) params.set("cursor", cursor)
      const filterStr = Object.entries(filters)
        .filter(([, v]) => v.trim() !== "")
        .map(([k, v]) => `${k}=${v}`)
        .join(",")
      if (filterStr) params.set("filter", filterStr)
      const res = await api.get<RecordsListResponse>(`/api/collections/${name}/records?${params}`)
      if (currentNameRef.current !== requestName) return
      setRows((prev) => (cursor ? [...(prev ?? []), ...res.items] : res.items))
      setNextCursor(res.nextCursor)
    } catch (err) {
      if (currentNameRef.current !== requestName) return
      setError(err instanceof ApiError ? err.message : "Failed to load records.")
    } finally {
      if (currentNameRef.current === requestName) {
        setLoading(false)
        setLoadingMore(false)
      }
    }
  }

  useEffect(() => {
    setRows(null)
    setSelected(new Set())
    setAppliedFilters({})
    setColumnFilters({})
    load(undefined, {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name])

  const filteredRows = useMemo(() => {
    if (!rows) return null
    if (!debouncedSearch.trim()) return rows
    const q = debouncedSearch.toLowerCase()
    return rows.filter((r) => fields.some((f) => String(r[f.name] ?? "").toLowerCase().includes(q)))
  }, [rows, debouncedSearch, fields])

  function applyFilters() {
    setAppliedFilters(columnFilters)
    setRows(null)
    load(undefined, columnFilters)
  }

  function clearFilters() {
    setColumnFilters({})
    setAppliedFilters({})
    setRows(null)
    load(undefined, {})
  }

  const inlineSave = useCallback(
    async (rowId: string, field: { name: string }, apiValue: unknown) => {
      const updated = await api.patch<RecordRow>(`/api/collections/${name}/records/${rowId}`, { [field.name]: apiValue })
      setRows((prev) => prev?.map((r) => (r.id === rowId ? updated : r)) ?? null)
      if (openRecord?.id === rowId) setOpenRecord(updated)
    },
    [name, openRecord],
  )

  // Memoized so RecordsTable (wrapped in React.memo) doesn't re-render on
  // every keystroke in the search box just because these callbacks got new
  // identities.
  const toggleSelect = useCallback((id: string, checked: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev)
      checked ? next.add(id) : next.delete(id)
      return next
    })
  }, [])

  const toggleAll = useCallback(
    (checked: boolean) => setSelected(checked ? new Set((filteredRows ?? []).map((r) => r.id)) : new Set()),
    [filteredRows],
  )

  if (collections !== null && !collection) return <Navigate to="/collections" replace />

  const meta = collection ? getMeta(collection.name) : null
  const activeFilterCount = Object.values(appliedFilters).filter((v) => v.trim() !== "").length

  return (
    <div className={cn("space-y-4", fullscreen && "fixed inset-0 z-50 overflow-auto bg-background p-4")}>
      {!fullscreen && (
        <Button asChild variant="ghost" size="sm" className="-ml-2 text-muted-foreground">
          <Link to={`/collections/${name}`}>
            <ArrowLeft />
            {name}
          </Link>
        </Button>
      )}

      <div className="flex items-center gap-2">
        {meta && <CollectionIcon icon={meta.icon} color={meta.color} className="size-8" iconClassName="size-4" />}
        <h2 className="text-lg font-semibold">{name}</h2>
        <span className="text-sm text-muted-foreground">{rows ? `${rows.length} loaded` : ""}</span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Input
          placeholder="Search loaded records…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-9 w-56"
        />

        <Popover>
          <PopoverTrigger asChild>
            <Button variant={activeFilterCount > 0 ? "secondary" : "outline"} size="sm" className="h-9">
              <FilterIcon className="size-3.5" />
              Filters
              {activeFilterCount > 0 && ` (${activeFilterCount})`}
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-72 space-y-3" align="start">
            <p className="text-xs text-muted-foreground">Exact-match filters, applied server-side.</p>
            {fields.map((f) => (
              <div key={f.name} className="space-y-1">
                <Label className="text-xs">{f.name}</Label>
                <Input
                  className="h-8"
                  value={columnFilters[f.name] ?? ""}
                  onChange={(e) => setColumnFilters((prev) => ({ ...prev, [f.name]: e.target.value }))}
                />
              </div>
            ))}
            <div className="flex gap-2">
              <Button size="sm" onClick={applyFilters}>
                Apply
              </Button>
              <Button size="sm" variant="ghost" onClick={clearFilters}>
                Clear
              </Button>
            </div>
          </PopoverContent>
        </Popover>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="sm" className="h-9">
              <Columns3 className="size-3.5" />
              Columns
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>Visible columns</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {fields.map((f) => (
              <DropdownMenuCheckboxItem
                key={f.name}
                checked={columnVisibility[f.name] !== false}
                onCheckedChange={(v) => setColumnVisibility((prev) => ({ ...prev, [f.name]: v }))}
                onSelect={(e) => e.preventDefault()}
              >
                {f.name}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <Select value={density} onValueChange={(v) => setDensity(v as Density)}>
          <SelectTrigger className="h-9 w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="compact">Compact</SelectItem>
            <SelectItem value="comfortable">Comfortable</SelectItem>
            <SelectItem value="spacious">Spacious</SelectItem>
          </SelectContent>
        </Select>

        <Button
          variant="outline"
          size="icon"
          className="size-9"
          onClick={() => setFullscreen((v) => !v)}
          aria-label={fullscreen ? "Exit fullscreen" : "Enter fullscreen"}
        >
          {fullscreen ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
        </Button>

        <Button variant="outline" size="icon" className="size-9" onClick={() => load()} title="Refresh" aria-label="Refresh">
          <RotateCw className="size-3.5" />
        </Button>

        <div className="ml-auto">
          <CreateRecordSheet
            collectionName={name}
            fields={fields}
            onCreated={(r) => setRows((prev) => [r, ...(prev ?? [])])}
            open={createOpen}
            onOpenChange={setCreateOpen}
            relationLabels={relationLabels}
          />
        </div>
      </div>

      {selected.size > 0 && (
        <BulkActionsBar
          collectionName={name}
          fields={fields}
          rows={rows ?? []}
          selectedIds={selected}
          onClear={() => setSelected(new Set())}
          onDeleted={(ids) => {
            setRows((prev) => prev?.filter((r) => !ids.includes(r.id)) ?? null)
            setSelected(new Set())
          }}
          onDuplicated={(records) => {
            setRows((prev) => [...records, ...(prev ?? [])])
            setSelected(new Set())
          }}
        />
      )}

      {error && (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <AlertTriangle />
            </EmptyMedia>
            <EmptyTitle>Couldn't load records</EmptyTitle>
            <EmptyDescription>{error}</EmptyDescription>
          </EmptyHeader>
          <Button size="sm" onClick={() => load()}>
            <RotateCw />
            Retry
          </Button>
        </Empty>
      )}

      {!error && loading && (
        <div className="space-y-2">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      )}

      {!error && !loading && filteredRows !== null && filteredRows.length === 0 && (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Database />
            </EmptyMedia>
            <EmptyTitle>{search || activeFilterCount ? "No matching records" : "No records yet"}</EmptyTitle>
            <EmptyDescription>
              {search || activeFilterCount
                ? "Try a different search or clear your filters."
                : "Create the first record in this collection."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}

      {!error && !loading && filteredRows && filteredRows.length > 0 && (
        <>
          <RecordsTable
            fields={fields}
            rows={filteredRows}
            selected={selected}
            onToggleSelect={toggleSelect}
            onToggleAll={toggleAll}
            onOpenRecord={setOpenRecord}
            onInlineSave={inlineSave}
            query={debouncedSearch}
            columnVisibility={columnVisibility}
            onColumnVisibilityChange={setColumnVisibility}
            density={density}
            sorting={sorting}
            onSortingChange={setSorting}
            relationLabels={relationLabels}
          />
          {nextCursor && (
            <div className="flex justify-center">
              <Button variant="outline" size="sm" onClick={() => load(nextCursor)} disabled={loadingMore}>
                {loadingMore && <Loader2 className="animate-spin" />}
                Load more
              </Button>
            </div>
          )}
        </>
      )}

      <RecordDetailSheet
        collectionName={name}
        fields={fields}
        record={openRecord}
        relationLabels={relationLabels}
        onOpenChange={(open) => !open && setOpenRecord(null)}
        onUpdated={(r) => {
          setRows((prev) => prev?.map((row) => (row.id === r.id ? r : row)) ?? null)
          setOpenRecord(r)
        }}
        onDeleted={(id) => {
          setRows((prev) => prev?.filter((r) => r.id !== id) ?? null)
          setOpenRecord(null)
          toast.dismiss()
        }}
      />
    </div>
  )
}
