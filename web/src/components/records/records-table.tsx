import { memo, useMemo, useState } from "react"
import {
  type ColumnDef,
  type SortingState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table"
import { ArrowDown, ArrowUp, ArrowUpDown, Loader2, Maximize2 } from "lucide-react"
import { Checkbox } from "@/components/ui/checkbox"
import { FieldCell } from "@/components/records/field-cell"
import { FieldInput } from "@/components/records/field-input"
import { formToApiValue, apiToFormValue } from "@/lib/record-form"
import { labelsFor, type RelationLabels } from "@/lib/relations"
import { cn } from "@/lib/utils"
import type { Field, RecordRow } from "@/lib/types"

export type Density = "compact" | "comfortable" | "spacious"

const DENSITY_PADDING: Record<Density, string> = {
  compact: "py-1",
  comfortable: "py-2",
  spacious: "py-3.5",
}

interface EditingCell {
  rowId: string
  fieldName: string
}

export const RecordsTable = memo(function RecordsTable({
  fields,
  rows,
  selected,
  onToggleSelect,
  onToggleAll,
  onOpenRecord,
  onInlineSave,
  query,
  columnVisibility,
  onColumnVisibilityChange,
  density,
  sorting,
  onSortingChange,
  relationLabels,
}: {
  fields: Field[]
  rows: RecordRow[]
  selected: Set<string>
  onToggleSelect: (id: string, checked: boolean) => void
  onToggleAll: (checked: boolean) => void
  onOpenRecord: (row: RecordRow) => void
  onInlineSave: (rowId: string, field: Field, apiValue: unknown) => Promise<void>
  query?: string
  columnVisibility: VisibilityState
  onColumnVisibilityChange: (v: VisibilityState) => void
  density: Density
  sorting: SortingState
  onSortingChange: (s: SortingState) => void
  // id -> label maps per relation target collection (see lib/relations.ts)
  // — optional so callers that never have a relation field don't need to
  // pass anything.
  relationLabels?: RelationLabels
}) {
  const [editing, setEditing] = useState<EditingCell | null>(null)
  const [draft, setDraft] = useState<unknown>(null)
  const [saving, setSaving] = useState(false)
  const [editError, setEditError] = useState<string | null>(null)

  function startEdit(row: RecordRow, field: Field) {
    setEditing({ rowId: row.id, fieldName: field.name })
    setDraft(apiToFormValue(field, row[field.name]))
    setEditError(null)
  }

  function cancelEdit() {
    setEditing(null)
    setDraft(null)
    setEditError(null)
  }

  async function commitEdit(row: RecordRow, field: Field) {
    try {
      const apiValue = formToApiValue(field, draft)
      if (apiValue === row[field.name]) {
        cancelEdit()
        return
      }
      setSaving(true)
      setEditError(null)
      await onInlineSave(row.id, field, apiValue)
      setEditing(null)
      setDraft(null)
    } catch (err) {
      setEditError(err instanceof Error ? err.message : "Failed to save")
    } finally {
      setSaving(false)
    }
  }

  const columns = useMemo<ColumnDef<RecordRow>[]>(() => {
    const selectCol: ColumnDef<RecordRow> = {
      id: "__select",
      size: 64,
      enableResizing: false,
      header: () => (
        <Checkbox
          checked={rows.length > 0 && rows.every((r) => selected.has(r.id))}
          onCheckedChange={(v) => onToggleAll(!!v)}
          aria-label="Select all rows"
        />
      ),
      cell: ({ row }) => (
        <div className="flex items-center gap-1.5">
          <Checkbox
            checked={selected.has(row.original.id)}
            onCheckedChange={(v) => onToggleSelect(row.original.id, !!v)}
            aria-label="Select row"
          />
          <button
            type="button"
            aria-label="Open record"
            title="Open record"
            onClick={() => onOpenRecord(row.original)}
            className="rounded p-0.5 text-muted-foreground opacity-0 hover:bg-accent hover:text-foreground group-hover:opacity-100"
          >
            <Maximize2 className="size-3.5" />
          </button>
        </div>
      ),
    }

    const fieldCols: ColumnDef<RecordRow>[] = fields.map((field) => ({
      id: field.name,
      accessorFn: (row) => row[field.name],
      size: 200,
      minSize: 100,
      header: field.name,
      cell: ({ row }) => {
        const isEditing = editing?.rowId === row.original.id && editing.fieldName === field.name
        const fieldRelationLabels = relationLabels ? labelsFor(relationLabels, field) : undefined
        if (isEditing) {
          return (
            <div className="flex items-center gap-1">
              <FieldInput
                field={field}
                value={draft}
                onChange={setDraft}
                autoFocus
                relationLabels={fieldRelationLabels}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && field.type !== "json") {
                    e.preventDefault()
                    commitEdit(row.original, field)
                  } else if (e.key === "Escape") {
                    cancelEdit()
                  }
                }}
              />
              {saving ? (
                <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
              ) : (
                <span className="size-1.5 shrink-0 rounded-full bg-amber-500" title="Unsaved" />
              )}
            </div>
          )
        }
        if (field.type === "relation") {
          return (
            <div className="px-1 py-0.5">
              <FieldCell
                field={field}
                value={row.original[field.name]}
                query={query}
                relationLabel={fieldRelationLabels?.get(String(row.original[field.name]))}
              />
            </div>
          )
        }
        return (
          <button
            type="button"
            className="block w-full truncate rounded px-1 py-0.5 text-left hover:bg-accent"
            onClick={(e) => {
              e.stopPropagation()
              startEdit(row.original, field)
            }}
          >
            <FieldCell field={field} value={row.original[field.name]} query={query} />
          </button>
        )
      },
    }))

    return [selectCol, ...fieldCols]
  }, [fields, rows, selected, editing, draft, saving, query, relationLabels])

  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting, columnVisibility },
    onSortingChange: (updater) => onSortingChange(typeof updater === "function" ? updater(sorting) : updater),
    onColumnVisibilityChange: (updater) =>
      onColumnVisibilityChange(typeof updater === "function" ? updater(columnVisibility) : updater),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    columnResizeMode: "onChange",
    enableColumnResizing: true,
  })

  return (
    <div className="relative overflow-auto rounded-lg border" style={{ maxHeight: "calc(100vh - 320px)" }}>
      <table className="w-full border-collapse text-sm" style={{ width: table.getTotalSize() }}>
        <thead className="sticky top-0 z-20 bg-background">
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b">
              {hg.headers.map((header, i) => (
                <th
                  key={header.id}
                  className={cn(
                    "relative select-none border-r px-3 py-2 text-left font-medium text-muted-foreground last:border-r-0",
                    i === 0 && "sticky left-0 z-10 bg-background",
                  )}
                  style={{ width: header.getSize() }}
                >
                  {header.isPlaceholder ? null : header.column.id === "__select" ? (
                    flexRender(header.column.columnDef.header, header.getContext())
                  ) : (
                    <button
                      type="button"
                      className="flex items-center gap-1 hover:text-foreground"
                      onClick={header.column.getToggleSortingHandler()}
                    >
                      {flexRender(header.column.columnDef.header, header.getContext())}
                      {{
                        asc: <ArrowUp className="size-3" />,
                        desc: <ArrowDown className="size-3" />,
                        false: <ArrowUpDown className="size-3 opacity-30" />,
                      }[(header.column.getIsSorted() as string) || "false"] ?? null}
                    </button>
                  )}
                  {header.column.getCanResize() && (
                    <div
                      onMouseDown={header.getResizeHandler()}
                      onTouchStart={header.getResizeHandler()}
                      className="absolute right-0 top-0 h-full w-1 cursor-col-resize touch-none select-none hover:bg-primary/50"
                    />
                  )}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr
              key={row.id}
              className={cn(
                "group border-b last:border-b-0 hover:bg-accent/50",
                selected.has(row.original.id) && "bg-accent/40",
              )}
            >
              {row.getVisibleCells().map((cell, i) => (
                <td
                  key={cell.id}
                  className={cn(
                    "border-r px-3 last:border-r-0",
                    DENSITY_PADDING[density],
                    i === 0 && "sticky left-0 z-10 bg-background",
                  )}
                  style={{ width: cell.column.getSize() }}
                >
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {editError && (
        <div className="sticky left-0 border-t bg-destructive/10 px-3 py-1.5 text-xs text-destructive">
          {editError}
        </div>
      )}
    </div>
  )
})
