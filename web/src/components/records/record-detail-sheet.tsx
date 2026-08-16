import { useEffect, useState } from "react"
import { toast } from "sonner"
import { Loader2, Pencil, Sparkles, Trash2 } from "lucide-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
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
import { FieldInput } from "@/components/records/field-input"
import { FieldCell, TypeBadge } from "@/components/records/field-cell"
import { apiToFormValue, formToApiValue } from "@/lib/record-form"
import { labelsFor, type RelationLabels } from "@/lib/relations"
import { formatRelativeTime } from "@/lib/format"
import { api, ApiError } from "@/lib/api"
import type { ChatbotResponse, Field, RecordRow } from "@/lib/types"

const RECORD_SUGGESTIONS = [
  { label: "Explain record", prompt: "Explain what this record represents in plain terms." },
  { label: "Detect anomalies", prompt: "Look for anything unusual, inconsistent, or out of range in this record." },
  { label: "Rewrite content", prompt: "Suggest a clearer, better-written version of this record's text fields." },
  { label: "Fill missing values", prompt: "Suggest reasonable values for any empty or missing fields on this record." },
]

export function RecordDetailSheet({
  collectionName,
  fields,
  record,
  onOpenChange,
  onUpdated,
  onDeleted,
  relationLabels,
}: {
  collectionName: string
  fields: Field[]
  record: RecordRow | null
  onOpenChange: (open: boolean) => void
  onUpdated: (r: RecordRow) => void
  onDeleted: (id: string) => void
  relationLabels?: RelationLabels
}) {
  const [editingField, setEditingField] = useState<string | null>(null)
  const [draft, setDraft] = useState<unknown>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)

  const [aiMessage, setAiMessage] = useState("Summarize this record and flag anything unusual.")
  const [aiReply, setAiReply] = useState<string | null>(null)
  const [aiLoading, setAiLoading] = useState(false)
  const [aiError, setAiError] = useState<string | null>(null)

  useEffect(() => {
    setEditingField(null)
    setAiReply(null)
    setAiError(null)
  }, [record?.id])

  if (!record) return null

  function startEdit(field: Field) {
    setEditingField(field.name)
    setDraft(apiToFormValue(field, record![field.name]))
    setError(null)
  }

  async function commit(field: Field) {
    try {
      const apiValue = formToApiValue(field, draft)
      if (apiValue === record![field.name]) {
        setEditingField(null)
        return
      }
      setSaving(true)
      setError(null)
      const updated = await api.patch<RecordRow>(`/api/collections/${collectionName}/records/${record!.id}`, {
        [field.name]: apiValue,
      })
      onUpdated(updated)
      setEditingField(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Failed to save")
    } finally {
      setSaving(false)
    }
  }

  async function doDelete() {
    setDeleting(true)
    try {
      await api.delete(`/api/collections/${collectionName}/records/${record!.id}`)
      toast.success("Record deleted")
      onDeleted(record!.id)
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Failed to delete record")
    } finally {
      setDeleting(false)
    }
  }

  async function askAi(text: string) {
    setAiLoading(true)
    setAiError(null)
    setAiReply(null)
    try {
      const res = await api.post<ChatbotResponse>("/api/chat", {
        message: text,
        context: { page: "records", collection: collectionName, record_id: record!.id },
      })
      setAiReply(res.reply)
    } catch (err) {
      setAiError(err instanceof ApiError ? err.message : "Failed to reach the AI copilot.")
    } finally {
      setAiLoading(false)
    }
  }

  return (
    <Sheet open={!!record} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-md">
        <SheetHeader>
          <SheetTitle className="font-mono text-sm">{record.id}</SheetTitle>
          <SheetDescription>Record in "{collectionName}"</SheetDescription>
        </SheetHeader>

        <div className="space-y-4 px-4 pb-4">
          <div className="grid grid-cols-3 gap-2 rounded-md border p-3 text-xs">
            <div>
              <p className="text-muted-foreground">Created</p>
              <p className="font-medium">{formatRelativeTime(record.created)}</p>
            </div>
            <div>
              <p className="text-muted-foreground">Updated</p>
              <p className="font-medium">{formatRelativeTime(record.updated)}</p>
            </div>
            <div>
              <p className="text-muted-foreground">Owner</p>
              <p className="truncate font-medium">{record.owner_id ? record.owner_id.slice(0, 8) : "—"}</p>
            </div>
          </div>

          <div className="space-y-3">
            {fields.map((f) => (
              <div key={f.name} className="space-y-1.5">
                <Label className="flex items-center gap-1.5 text-xs">
                  {f.name}
                  <TypeBadge type={f.type} />
                </Label>
                {editingField === f.name ? (
                  <div className="flex items-center gap-1.5">
                    <FieldInput
                      field={f}
                      value={draft}
                      onChange={setDraft}
                      autoFocus
                      className="flex-1"
                      relationLabels={f.type === "relation" ? labelsFor(relationLabels ?? {}, f) : undefined}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && f.type !== "json") {
                          e.preventDefault()
                          commit(f)
                        } else if (e.key === "Escape") {
                          setEditingField(null)
                        }
                      }}
                    />
                    <Button size="sm" onClick={() => commit(f)} disabled={saving}>
                      {saving ? <Loader2 className="animate-spin" /> : "Save"}
                    </Button>
                  </div>
                ) : f.type === "relation" && record[f.name] ? (
                  <div className="flex items-center gap-1.5 rounded-md border border-transparent px-2 py-1.5 hover:border-input hover:bg-accent">
                    <div className="flex-1 text-sm">
                      <FieldCell
                        field={f}
                        value={record[f.name]}
                        relationLabel={labelsFor(relationLabels ?? {}, f)?.get(String(record[f.name]))}
                      />
                    </div>
                    <button
                      type="button"
                      onClick={() => startEdit(f)}
                      aria-label={`Edit ${f.name}`}
                      className="shrink-0 text-muted-foreground hover:text-foreground"
                    >
                      <Pencil className="size-3.5" />
                    </button>
                  </div>
                ) : (
                  <button
                    type="button"
                    onClick={() => startEdit(f)}
                    className="w-full rounded-md border border-transparent px-2 py-1.5 text-left text-sm hover:border-input hover:bg-accent"
                  >
                    {record[f.name] === null || record[f.name] === undefined || record[f.name] === "" ? (
                      <span className="text-muted-foreground">Click to set…</span>
                    ) : f.type === "json" ? (
                      <code className="font-mono text-xs">{JSON.stringify(record[f.name])}</code>
                    ) : (
                      String(record[f.name])
                    )}
                  </button>
                )}
              </div>
            ))}
            {error && <p className="text-xs text-destructive">{error}</p>}
          </div>

          <Separator />

          <div className="space-y-2">
            <Label className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Sparkles className="size-3.5" />
              Ask AI about this record
            </Label>
            <div className="flex flex-wrap gap-1.5">
              {RECORD_SUGGESTIONS.map((s) => (
                <Button
                  key={s.label}
                  size="sm"
                  variant="outline"
                  className="h-7 text-xs"
                  disabled={aiLoading}
                  onClick={() => {
                    setAiMessage(s.prompt)
                    askAi(s.prompt)
                  }}
                >
                  {s.label}
                </Button>
              ))}
            </div>
            <Textarea value={aiMessage} onChange={(e) => setAiMessage(e.target.value)} rows={2} />
            <Button size="sm" variant="outline" onClick={() => askAi(aiMessage)} disabled={aiLoading || !aiMessage.trim()}>
              {aiLoading ? <Loader2 className="animate-spin" /> : <Sparkles />}
              Ask
            </Button>
            {aiError && <p className="text-xs text-destructive">{aiError}</p>}
            {aiReply && <p className="whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-xs">{aiReply}</p>}
          </div>
        </div>

        <SheetFooter className="px-4">
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="outline" className="text-destructive hover:text-destructive">
                <Trash2 />
                Delete record
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete this record?</AlertDialogTitle>
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
