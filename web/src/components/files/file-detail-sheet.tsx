import { useEffect, useState } from "react"
import { toast } from "sonner"
import { AlertCircle, Download, Loader2, Pin, Sparkles, Star, Trash2, X } from "lucide-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Textarea } from "@/components/ui/textarea"
import { Separator } from "@/components/ui/separator"
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
import { FileIcon } from "@/components/files/file-icon"
import { FilePreview } from "@/components/files/file-preview"
import { COLLECTION_COLORS } from "@/lib/collection-meta"
import { colorClasses } from "@/lib/collection-icons"
import { getFileMeta, setFileMeta, toggleFileFavorite, toggleFilePinned, useFileMetaStore } from "@/lib/file-meta"
import { formatBytes, isAiReadable } from "@/lib/file-render"
import { formatRelativeTime } from "@/lib/format"
import { api, fetchBlob, ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ChatbotResponse, FileRecord } from "@/lib/types"

const AI_PRESETS = [
  { label: "Summarize", message: "Summarize this file." },
  { label: "Explain", message: "Explain what this file is and what it's for." },
  { label: "Key points", message: "Extract the key points from this file as a short bulleted list." },
  { label: "Extract entities", message: "Extract the key entities from this file (people, organizations, dates, amounts, etc.) as a list." },
]

export function FileDetailSheet({
  file,
  onOpenChange,
  onDeleted,
}: {
  file: FileRecord | null
  onOpenChange: (open: boolean) => void
  onDeleted: (id: string) => void
}) {
  useFileMetaStore()
  const [deleting, setDeleting] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [tagInput, setTagInput] = useState("")

  const [aiMessage, setAiMessage] = useState("")
  const [aiReply, setAiReply] = useState<string | null>(null)
  const [aiLoading, setAiLoading] = useState(false)
  const [aiError, setAiError] = useState<string | null>(null)

  useEffect(() => {
    setAiMessage("")
    setAiReply(null)
    setAiError(null)
    setTagInput("")
  }, [file?.id])

  if (!file) return null
  const meta = getFileMeta(file.id)
  const aiReady = isAiReadable(file.filename)

  async function download() {
    setDownloading(true)
    try {
      const blob = await fetchBlob(`/api/files/${file!.id}`)
      const url = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = url
      a.download = file!.filename
      a.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Failed to download file")
    } finally {
      setDownloading(false)
    }
  }

  async function doDelete() {
    setDeleting(true)
    try {
      await api.delete(`/api/files/${file!.id}`)
      toast.success("File deleted")
      onDeleted(file!.id)
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Failed to delete file")
    } finally {
      setDeleting(false)
    }
  }

  async function ask(message: string) {
    setAiLoading(true)
    setAiError(null)
    setAiReply(null)
    try {
      const res = await api.post<ChatbotResponse>("/api/chat", { message, attachment_ids: [file!.id] })
      setAiReply(res.reply)
    } catch (err) {
      setAiError(err instanceof ApiError ? err.message : "Failed to reach the AI copilot.")
    } finally {
      setAiLoading(false)
    }
  }

  function addTag() {
    const t = tagInput.trim()
    if (!t || meta.tags.includes(t)) return
    setFileMeta(file!.id, { tags: [...meta.tags, t] })
    setTagInput("")
  }

  return (
    <Sheet open={!!file} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-md">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2 pr-6">
            {/* Icon reflects file type only — the color swatch picker
                below is where the organizational color actually shows. */}
            <FileIcon mime={file.mime} filename={file.filename} className="size-8" iconClassName="size-4" />
            <span className="truncate">{file.filename}</span>
          </SheetTitle>
          <SheetDescription>
            {formatBytes(file.size)} · {file.mime}
          </SheetDescription>
        </SheetHeader>

        <div className="space-y-4 px-4 pb-4">
          <div className="flex items-center gap-1.5">
            <Button variant={meta.favorite ? "secondary" : "outline"} size="sm" onClick={() => toggleFileFavorite(file!.id)}>
              <Star className={cn("size-3.5", meta.favorite && "fill-amber-400 text-amber-400")} />
              Favorite
            </Button>
            <Button variant={meta.pinned ? "secondary" : "outline"} size="sm" onClick={() => toggleFilePinned(file!.id)}>
              <Pin className={cn("size-3.5", meta.pinned && "fill-current")} />
              Pin
            </Button>
          </div>

          <FilePreview file={file} />

          <div className="grid grid-cols-2 gap-2 rounded-md border p-3 text-xs">
            <div>
              <p className="text-muted-foreground">Created</p>
              <p className="font-medium">{formatRelativeTime(file.created)}</p>
            </div>
            <div>
              <p className="text-muted-foreground">Owner</p>
              <p className="truncate font-medium">{file.owner_id ? file.owner_id.slice(0, 8) : "—"}</p>
            </div>
          </div>

          <div className="flex items-center gap-2 rounded-md border p-3 text-xs">
            {aiReady ? (
              <>
                <Sparkles className="size-3.5 text-primary" />
                <span>AI can read this file's content when asked about it.</span>
              </>
            ) : (
              <>
                <AlertCircle className="size-3.5 text-muted-foreground" />
                <span className="text-muted-foreground">
                  AI can't extract this file type yet — supported: images, PDF, DOCX, TXT, MD, CSV, XLSX.
                </span>
              </>
            )}
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs">Color</Label>
            <div className="flex flex-wrap gap-1.5">
              <button
                type="button"
                onClick={() => setFileMeta(file!.id, { color: null })}
                className={cn("size-6 rounded-full border border-dashed", !meta.color && "ring-2 ring-ring ring-offset-2 ring-offset-background")}
                aria-label="No color"
              />
              {COLLECTION_COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  onClick={() => setFileMeta(file!.id, { color: c })}
                  className={cn("size-6 rounded-full", colorClasses(c).split(" ")[0], meta.color === c && "ring-2 ring-ring ring-offset-2 ring-offset-background")}
                  aria-label={c}
                />
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs">Tags</Label>
            <div className="flex flex-wrap gap-1.5">
              {meta.tags.map((t) => (
                <Badge key={t} variant="secondary" className="gap-1 font-normal">
                  {t}
                  <button
                    type="button"
                    onClick={() => setFileMeta(file!.id, { tags: meta.tags.filter((x) => x !== t) })}
                    aria-label={`Remove tag ${t}`}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
            </div>
            <div className="flex gap-1.5">
              <Input
                value={tagInput}
                onChange={(e) => setTagInput(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && (e.preventDefault(), addTag())}
                placeholder="Add a tag…"
                className="h-8"
              />
              <Button size="sm" variant="outline" onClick={addTag}>
                Add
              </Button>
            </div>
          </div>

          <Separator />

          <div className="space-y-2">
            <Label className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Sparkles className="size-3.5" />
              Ask AI about this file
            </Label>
            <div className="flex flex-wrap gap-1.5">
              {AI_PRESETS.map((p) => (
                <Button key={p.label} size="sm" variant="outline" disabled={!aiReady || aiLoading} onClick={() => ask(p.message)}>
                  {p.label}
                </Button>
              ))}
            </div>
            <div className="flex gap-1.5">
              <Textarea
                value={aiMessage}
                onChange={(e) => setAiMessage(e.target.value)}
                placeholder="Ask something specific…"
                rows={2}
                disabled={!aiReady}
              />
            </div>
            <Button size="sm" onClick={() => ask(aiMessage)} disabled={!aiReady || aiLoading || !aiMessage.trim()}>
              {aiLoading ? <Loader2 className="animate-spin" /> : <Sparkles />}
              Ask
            </Button>
            {aiError && <p className="text-xs text-destructive">{aiError}</p>}
            {aiReply && <p className="whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-xs">{aiReply}</p>}
          </div>
        </div>

        <SheetFooter className="flex-row gap-2 px-4">
          <Button variant="outline" className="flex-1" onClick={download} disabled={downloading}>
            {downloading ? <Loader2 className="animate-spin" /> : <Download />}
            Download
          </Button>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="outline" className="text-destructive hover:text-destructive">
                <Trash2 />
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete "{file.filename}"?</AlertDialogTitle>
                <AlertDialogDescription>This can't be undone.</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  disabled={deleting}
                  onClick={(e) => {
                    e.preventDefault()
                    doDelete()
                  }}
                  className="bg-destructive text-white hover:bg-destructive/90"
                >
                  {deleting && <Loader2 className="animate-spin" />}
                  Delete
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
