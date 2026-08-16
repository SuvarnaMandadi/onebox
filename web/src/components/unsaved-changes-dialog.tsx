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
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"

// Shared by create/edit-collection-dialog (RC2): closing a form dialog
// (the X button, Escape, or a click outside) while it has unsaved changes
// should never silently discard them — ask Save / Discard / Cancel,
// exactly what a form-heavy desktop app is expected to do. Deliberately
// standalone from AlertDialog's own open state — the parent dialog decides
// when to show this (on a close attempt while dirty), not this component.
export function UnsavedChangesDialog({
  open,
  onOpenChange,
  onSave,
  onDiscard,
  saving,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: () => void
  onDiscard: () => void
  saving?: boolean
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Save changes?</AlertDialogTitle>
          <AlertDialogDescription>You have unsaved changes. Save them before closing, or discard them.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant="destructive" onClick={onDiscard} disabled={saving}>
            Discard changes
          </Button>
          <AlertDialogAction onClick={onSave} disabled={saving}>
            {saving && <Loader2 className="animate-spin" />}
            Save changes
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
