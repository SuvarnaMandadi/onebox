import { AlertTriangle, Check, Loader2, RotateCw, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { cancelUpload, clearFinishedUploads, dismissUpload, retryUpload, useUploadQueue } from "@/lib/upload-manager"
import { formatBytes } from "@/lib/file-render"

export function UploadQueuePanel() {
  const items = useUploadQueue()
  if (items.length === 0) return null

  const active = items.filter((it) => it.status === "queued" || it.status === "uploading")
  const finished = items.filter((it) => it.status !== "queued" && it.status !== "uploading")

  return (
    <div className="fixed bottom-4 right-4 z-40 w-80 space-y-1 rounded-lg border bg-card p-3 shadow-lg">
      <div className="flex items-center justify-between">
        <p className="text-sm font-medium">
          Uploads {active.length > 0 && `(${active.length} in progress)`}
        </p>
        {finished.length > 0 && (
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={clearFinishedUploads}>
            Clear finished
          </Button>
        )}
      </div>
      <div className="max-h-64 space-y-2 overflow-y-auto">
        {items.map((it) => (
          <div key={it.id} className="space-y-1 rounded-md border p-2 text-xs">
            <div className="flex items-center justify-between gap-2">
              <span className="min-w-0 flex-1 truncate">{it.file.name}</span>
              <span className="shrink-0 text-muted-foreground">{formatBytes(it.file.size)}</span>
              {it.status === "uploading" && (
                <Button variant="ghost" size="icon" className="size-5" aria-label="Cancel upload" onClick={() => cancelUpload(it.id)}>
                  <X className="size-3" />
                </Button>
              )}
              {it.status === "done" && <Check className="size-3.5 shrink-0 text-green-600" />}
              {(it.status === "error" || it.status === "canceled") && (
                <>
                  <Button variant="ghost" size="icon" className="size-5" aria-label="Retry upload" onClick={() => retryUpload(it.id)}>
                    <RotateCw className="size-3" />
                  </Button>
                  <Button variant="ghost" size="icon" className="size-5" aria-label="Dismiss" onClick={() => dismissUpload(it.id)}>
                    <X className="size-3" />
                  </Button>
                </>
              )}
            </div>
            {it.status === "uploading" && <Progress value={it.progress} className="h-1" />}
            {it.status === "queued" && (
              <p className="flex items-center gap-1 text-muted-foreground">
                <Loader2 className="size-3 animate-spin" /> Waiting…
              </p>
            )}
            {it.status === "error" && (
              <p className="flex items-center gap-1 text-destructive">
                <AlertTriangle className="size-3" /> {it.error}
              </p>
            )}
            {it.status === "canceled" && <p className="text-muted-foreground">Canceled</p>}
          </div>
        ))}
      </div>
    </div>
  )
}
