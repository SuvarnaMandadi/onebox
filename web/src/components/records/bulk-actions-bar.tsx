import { useState } from "react"
import { toast } from "sonner"
import { Copy, Download, Loader2, Sparkles, Trash2, X, Copy as DuplicateIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { api } from "@/lib/api"
import type { Field, RecordRow } from "@/lib/types"

export function BulkActionsBar({
  collectionName,
  fields,
  rows,
  selectedIds,
  onClear,
  onDeleted,
  onDuplicated,
}: {
  collectionName: string
  fields: Field[]
  rows: RecordRow[]
  selectedIds: Set<string>
  onClear: () => void
  onDeleted: (ids: string[]) => void
  onDuplicated: (records: RecordRow[]) => void
}) {
  const [deleting, setDeleting] = useState(false)
  const [duplicating, setDuplicating] = useState(false)
  const selected = rows.filter((r) => selectedIds.has(r.id))
  const count = selected.length
  if (count === 0) return null

  async function deleteSelected() {
    setDeleting(true)
    const ids = selected.map((r) => r.id)
    const failures: string[] = []
    await Promise.all(
      ids.map((id) =>
        api.delete(`/api/collections/${collectionName}/records/${id}`).catch(() => {
          failures.push(id)
        }),
      ),
    )
    setDeleting(false)
    const succeeded = ids.filter((id) => !failures.includes(id))
    if (succeeded.length) onDeleted(succeeded)
    if (failures.length) toast.error(`Failed to delete ${failures.length} record(s)`)
    else toast.success(`Deleted ${succeeded.length} record(s)`)
  }

  async function duplicateSelected() {
    setDuplicating(true)
    const created: RecordRow[] = []
    let failed = 0
    for (const r of selected) {
      const input = Object.fromEntries(fields.map((f) => [f.name, r[f.name] ?? null]))
      try {
        const rec = await api.post<RecordRow>(`/api/collections/${collectionName}/records`, input)
        created.push(rec)
      } catch {
        failed++
      }
    }
    setDuplicating(false)
    if (created.length) onDuplicated(created)
    if (failed) toast.error(`Failed to duplicate ${failed} record(s)`)
    else toast.success(`Duplicated ${created.length} record(s)`)
  }

  function exportSelected() {
    const blob = new Blob([JSON.stringify(selected, null, 2)], { type: "application/json" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `${collectionName}-export.json`
    a.click()
    URL.revokeObjectURL(url)
    toast.success(`Exported ${count} record(s)`)
  }

  function copyIds() {
    navigator.clipboard.writeText(selected.map((r) => r.id).join("\n"))
    toast.success("Record IDs copied")
  }

  return (
    <div className="flex items-center gap-2 rounded-lg border bg-card px-3 py-2 shadow-sm">
      <span className="text-sm font-medium">{count} selected</span>
      <div className="ml-2 flex items-center gap-1">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Duplicate" onClick={duplicateSelected} disabled={duplicating}>
              {duplicating ? <Loader2 className="animate-spin" /> : <DuplicateIcon />}
            </Button>
          </TooltipTrigger>
          <TooltipContent>Duplicate</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Export as JSON" onClick={exportSelected}>
              <Download />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Export as JSON</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Copy IDs" onClick={copyIds}>
              <Copy />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Copy IDs</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="AI bulk actions — coming soon" disabled>
              <Sparkles />
            </Button>
          </TooltipTrigger>
          <TooltipContent>AI bulk actions — coming soon</TooltipContent>
        </Tooltip>
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Delete selected" className="text-destructive hover:text-destructive">
              <Trash2 />
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete {count} record(s)?</AlertDialogTitle>
              <AlertDialogDescription>This can't be undone.</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                disabled={deleting}
                onClick={(e) => {
                  e.preventDefault()
                  deleteSelected()
                }}
                className="bg-destructive text-white hover:bg-destructive/90"
              >
                {deleting && <Loader2 className="animate-spin" />}
                Delete
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
      <Button variant="ghost" size="icon" className="ml-auto" onClick={onClear} aria-label="Clear selection">
        <X />
      </Button>
    </div>
  )
}
