import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Copy, Download, Loader2, Sparkles, Trash2, X } from "lucide-react"
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
import { api, fetchBlob } from "@/lib/api"
import type { FileRecord } from "@/lib/types"

export function FilesBulkActionsBar({
  files,
  selectedIds,
  onClear,
  onDeleted,
}: {
  files: FileRecord[]
  selectedIds: Set<string>
  onClear: () => void
  onDeleted: (ids: string[]) => void
}) {
  const [deleting, setDeleting] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const navigate = useNavigate()
  const selected = files.filter((f) => selectedIds.has(f.id))
  const count = selected.length
  if (count === 0) return null

  // Opens the AI Workspace with these files pre-attached (via the same
  // attachment_ids the composer already sends — see composer.tsx's
  // ?attach handling) and a prefilled compare prompt. Genuine comparison
  // needs the files actually attached, not just named in text — there's
  // no read_file tool the model could use to pull their content itself.
  function compareInAi() {
    const ids = selected.map((f) => f.id).join(",")
    const prompt = `Compare these files: ${selected.map((f) => f.filename).join(", ")}. Note similarities, differences, and anything that stands out.`
    navigate(`/ai?attach=${encodeURIComponent(ids)}&prefill=${encodeURIComponent(prompt)}`)
  }

  async function deleteSelected() {
    setDeleting(true)
    const ids = selected.map((f) => f.id)
    const failures: string[] = []
    await Promise.all(
      ids.map((id) => api.delete(`/api/files/${id}`).catch(() => failures.push(id))),
    )
    setDeleting(false)
    const succeeded = ids.filter((id) => !failures.includes(id))
    if (succeeded.length) onDeleted(succeeded)
    if (failures.length) toast.error(`Failed to delete ${failures.length} file(s)`)
    else toast.success(`Deleted ${succeeded.length} file(s)`)
  }

  // There's no server-side zip/bulk-download endpoint — this downloads
  // each selected file individually (sequentially, so the browser doesn't
  // choke on N simultaneous blob downloads), not a single archive.
  async function downloadSelected() {
    setDownloading(true)
    let failed = 0
    for (const f of selected) {
      try {
        const blob = await fetchBlob(`/api/files/${f.id}`)
        const url = URL.createObjectURL(blob)
        const a = document.createElement("a")
        a.href = url
        a.download = f.filename
        a.click()
        URL.revokeObjectURL(url)
      } catch {
        failed++
      }
    }
    setDownloading(false)
    if (failed) toast.error(`Failed to download ${failed} file(s)`)
    else toast.success(`Downloaded ${selected.length} file(s)`)
  }

  function copyIds() {
    navigator.clipboard.writeText(selected.map((f) => f.id).join("\n"))
    toast.success("File IDs copied")
  }

  return (
    <div className="flex items-center gap-2 rounded-lg border bg-card px-3 py-2 shadow-sm">
      <span className="text-sm font-medium">{count} selected</span>
      <div className="ml-2 flex items-center gap-1">
        {count >= 2 && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="Compare with AI" onClick={compareInAi}>
                <Sparkles />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Compare with AI</TooltipContent>
          </Tooltip>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Download selected" onClick={downloadSelected} disabled={downloading}>
              {downloading ? <Loader2 className="animate-spin" /> : <Download />}
            </Button>
          </TooltipTrigger>
          <TooltipContent>Download (individually)</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Copy IDs" onClick={copyIds}>
              <Copy />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Copy IDs</TooltipContent>
        </Tooltip>
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Delete selected" className="text-destructive hover:text-destructive">
              <Trash2 />
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete {count} file(s)?</AlertDialogTitle>
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
