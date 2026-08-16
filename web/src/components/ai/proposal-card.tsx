import { useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import { AlertTriangle, Check, ExternalLink, Loader2, TriangleAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
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
import { useCollections } from "@/lib/collections-store"
import { api, ApiError } from "@/lib/api"
import type { ProposedAction } from "@/lib/ai-types"

// A proposedAction the model called but that autoExecutable
// (chatbot_tool_execution.go) did NOT run automatically — either because
// it's destructive (needs confirmation) or because it has no execution
// primitive on the backend at all. Only delete_collection and
// delete_field carry enough payload to actually execute here, reusing
// the exact same endpoints the Collections workspace's own delete/edit
// dialogs call — never a separate execution path. The others (rename,
// update_schema, import_data) genuinely have no backend primitive today
// (see executeToolCall's switch — it only ever handles 4 types), so this
// is honest about that and links to where the admin CAN do it by hand.
export function ProposalCard({ action }: { action: ProposedAction }) {
  const { refresh } = useCollections()
  const [done, setDone] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const payload = action.payload ?? {}

  async function executeDeleteCollection() {
    setBusy(true)
    setError(null)
    try {
      await api.delete(`/api/collections/${payload.name}`)
      await refresh()
      setDone(true)
      toast.success(`Deleted collection "${payload.name}"`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to execute")
    } finally {
      setBusy(false)
    }
  }

  async function executeDeleteField() {
    setBusy(true)
    setError(null)
    try {
      const res = await api.get<{ items: { name: string; schema: { fields: { name: string; type: string; required: boolean }[] } }[] }>(
        "/api/collections",
      )
      const col = res.items.find((c) => c.name === payload.collection)
      if (!col) throw new Error(`Collection "${payload.collection}" not found`)
      const fields = col.schema.fields.filter((f) => f.name !== payload.field)
      await api.patch(`/api/collections/${payload.collection}`, { fields })
      await refresh()
      setDone(true)
      toast.success(`Removed field "${payload.field}" from "${payload.collection}"`)
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : "Failed to execute")
    } finally {
      setBusy(false)
    }
  }

  const collectionLink = typeof payload.collection === "string" ? payload.collection : typeof payload.name === "string" ? payload.name : null

  return (
    <div className="w-full max-w-md rounded-lg border bg-card p-3 text-sm shadow-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-1.5 font-medium">
          {action.destructive && <TriangleAlert className="size-3.5 text-destructive" />}
          {action.title}
        </div>
        <Badge variant={action.destructive ? "destructive" : "secondary"} className="font-normal">
          {action.destructive ? "Needs confirmation" : "Not executed"}
        </Badge>
      </div>
      <p className="mt-1 text-xs text-muted-foreground">{action.description}</p>

      {done ? (
        <p className="mt-2 flex items-center gap-1.5 text-xs text-green-600 dark:text-green-500">
          <Check className="size-3.5" /> Done
        </p>
      ) : (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          {action.type === "delete_collection" && (
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button size="sm" variant="destructive" disabled={busy}>
                  {busy && <Loader2 className="animate-spin" />}
                  Confirm delete
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>Delete collection "{String(payload.name)}"?</AlertDialogTitle>
                  <AlertDialogDescription>This permanently deletes it and every record in it.</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={(e) => {
                      e.preventDefault()
                      executeDeleteCollection()
                    }}
                    className="bg-destructive text-white hover:bg-destructive/90"
                  >
                    Delete
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
          {action.type === "delete_field" && (
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button size="sm" variant="destructive" disabled={busy}>
                  {busy && <Loader2 className="animate-spin" />}
                  Confirm delete field
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    Delete field "{String(payload.field)}" from "{String(payload.collection)}"?
                  </AlertDialogTitle>
                  <AlertDialogDescription>This deletes that field's data for every record.</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={(e) => {
                      e.preventDefault()
                      executeDeleteField()
                    }}
                    className="bg-destructive text-white hover:bg-destructive/90"
                  >
                    Delete field
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
          {(action.type === "rename_collection" || action.type === "update_schema" || action.type === "import_data") && (
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <AlertTriangle className="size-3.5 shrink-0" />
              OneBox doesn't execute this from chat yet — do it from the collection page.
            </p>
          )}
          {collectionLink && (
            <Button asChild size="sm" variant="outline">
              <Link to={`/collections/${collectionLink}`}>
                <ExternalLink />
                Open {collectionLink}
              </Link>
            </Button>
          )}
        </div>
      )}
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
    </div>
  )
}
