import { useState, type ReactNode } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Loader2 } from "lucide-react"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { removeMeta } from "@/lib/collection-meta"
import { useCollections } from "@/lib/collections-store"
import { api, ApiError } from "@/lib/api"

export function DeleteCollectionDialog({ collectionName, children }: { collectionName: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const [confirmText, setConfirmText] = useState("")
  const [deleting, setDeleting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { refresh } = useCollections()
  const navigate = useNavigate()

  async function onConfirm() {
    setDeleting(true)
    setError(null)
    try {
      await api.delete(`/api/collections/${collectionName}`)
      removeMeta(collectionName)
      await refresh()
      toast.success(`Collection "${collectionName}" deleted`)
      setOpen(false)
      navigate("/collections")
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to delete collection.")
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) {
          setConfirmText("")
          setError(null)
        }
      }}
    >
      <AlertDialogTrigger asChild>{children}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete "{collectionName}"?</AlertDialogTitle>
          <AlertDialogDescription>
            This permanently deletes the collection and every record in it. This can't be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-2">
          <Label htmlFor="confirm-name" className="text-xs text-muted-foreground">
            Type <span className="font-mono font-medium text-foreground">{collectionName}</span> to confirm
          </Label>
          <Input id="confirm-name" value={confirmText} onChange={(e) => setConfirmText(e.target.value)} autoComplete="off" />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={confirmText !== collectionName || deleting}
            onClick={(e) => {
              e.preventDefault()
              onConfirm()
            }}
            className="bg-destructive text-white hover:bg-destructive/90"
          >
            {deleting && <Loader2 className="animate-spin" />}
            Delete collection
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
