import { useState, type ReactNode } from "react"
import { useForm, useFieldArray, Controller } from "react-hook-form"
import { toast } from "sonner"
import { Loader2, Plus, TriangleAlert, Trash2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
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
import { IconColorPicker } from "@/components/icon-color-picker"
import { UnsavedChangesDialog } from "@/components/unsaved-changes-dialog"
import { FieldValidationPopover } from "@/components/field-validation-popover"
import { getMeta, setMeta } from "@/lib/collection-meta"
import { useCollections } from "@/lib/collections-store"
import { validateFields } from "@/lib/field-validation"
import { FIELD_TYPES } from "@/lib/field-types"
import { api, ApiError } from "@/lib/api"
import type { Collection, Field } from "@/lib/types"

const NAME_RE = /^[a-zA-Z][a-zA-Z0-9_]{0,62}$/

interface FormValues {
  description: string
  icon: string
  color: string
  fields: Field[]
}

export function EditCollectionDialog({ collection, children }: { collection: Collection; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [confirmClose, setConfirmClose] = useState(false)
  const [removeIndex, setRemoveIndex] = useState<number | null>(null)
  const { collections, refresh } = useCollections()
  const meta = getMeta(collection.name)

  const {
    register,
    handleSubmit,
    control,
    reset,
    watch,
    setValue,
    formState: { errors, isSubmitting, isDirty },
  } = useForm<FormValues>({
    defaultValues: {
      description: meta.description,
      icon: meta.icon,
      color: meta.color,
      fields: collection.schema.fields,
    },
  })
  const { fields, append, remove } = useFieldArray({ control, name: "fields" })
  const icon = watch("icon")
  const color = watch("color")

  const removedCount = collection.schema.fields.filter((f) => !fields.some((g) => g.name === f.name)).length

  // RC2: closing (X, Escape, outside click) with unsaved changes must ask
  // first, not silently discard — Dialog's own onOpenChange below
  // intercepts a close attempt while isDirty and opens this instead of
  // actually closing; Discard/Save resolve it for real.
  function requestClose() {
    if (isDirty) {
      setConfirmClose(true)
      return
    }
    setOpen(false)
  }

  async function onSubmit(values: FormValues) {
    setSubmitError(null)
    for (const f of values.fields) {
      if (!NAME_RE.test(f.name)) {
        setSubmitError(`Invalid field name "${f.name}" — letters, digits, underscore, must start with a letter.`)
        return
      }
    }
    const fieldsError = validateFields(values.fields)
    if (fieldsError) {
      setSubmitError(fieldsError)
      return
    }
    try {
      const fieldsChanged =
        JSON.stringify(values.fields) !== JSON.stringify(collection.schema.fields)
      if (fieldsChanged) {
        await api.patch(`/api/collections/${collection.name}`, { fields: values.fields })
      }
      setMeta(collection.name, { description: values.description, icon: values.icon, color: values.color })
      await refresh()
      toast.success("Collection updated")
      setOpen(false)
    } catch (err) {
      setSubmitError(err instanceof ApiError ? err.message : "Failed to update collection.")
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setOpen(true)
          reset({
            description: meta.description,
            icon: meta.icon,
            color: meta.color,
            fields: collection.schema.fields,
          })
          setSubmitError(null)
        } else {
          requestClose()
        }
      }}
    >
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Edit {collection.name}</DialogTitle>
          <DialogDescription>
            The collection name can't be changed — it's also the API path (
            <code className="text-xs">/api/collections/{collection.name}</code>).
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-5">
          <div className="flex gap-3">
            <IconColorPicker icon={icon} color={color} onIconChange={(v) => setValue("icon", v)} onColorChange={(v) => setValue("color", v)} />
            <div className="flex-1 space-y-2">
              <Label>Name</Label>
              <Input value={collection.name} disabled />
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="ec-description">Description</Label>
            <Textarea id="ec-description" rows={2} {...register("description")} />
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>Fields</Label>
              <Button type="button" variant="outline" size="sm" onClick={() => append({ name: "", type: "text", required: false })}>
                <Plus />
                Add field
              </Button>
            </div>
            <div className="space-y-2">
              {fields.map((f, i) => {
                const fieldType = watch(`fields.${i}.type`)
                return (
                  <div key={f.id} className="flex flex-wrap items-start gap-2">
                    <Input placeholder="field_name" {...register(`fields.${i}.name` as const)} className="flex-1 basis-32" />
                    <Controller
                      control={control}
                      name={`fields.${i}.type` as const}
                      render={({ field }) => (
                        <Select
                          value={field.value}
                          onValueChange={(v) => {
                            field.onChange(v)
                            if (v !== "relation") setValue(`fields.${i}.relation_collection`, undefined)
                          }}
                        >
                          <SelectTrigger className="w-28">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {FIELD_TYPES.map((t) => (
                              <SelectItem key={t} value={t}>
                                {t}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      )}
                    />
                    {fieldType === "relation" && (
                      <Controller
                        control={control}
                        name={`fields.${i}.relation_collection` as const}
                        render={({ field }) => (
                          <Select value={field.value ?? ""} onValueChange={field.onChange}>
                            <SelectTrigger className="w-36">
                              <SelectValue placeholder="Target collection" />
                            </SelectTrigger>
                            <SelectContent>
                              {(collections ?? []).map((c: Collection) => (
                                <SelectItem key={c.name} value={c.name}>
                                  {c.name}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                      />
                    )}
                    <label className="flex h-9 items-center gap-1.5 text-xs text-muted-foreground">
                      <Controller
                        control={control}
                        name={`fields.${i}.required` as const}
                        render={({ field }) => <Checkbox checked={field.value} onCheckedChange={field.onChange} />}
                      />
                      required
                    </label>
                    <Controller
                      control={control}
                      name={`fields.${i}.validation` as const}
                      render={({ field }) => (
                        <FieldValidationPopover type={fieldType} value={field.value} onChange={field.onChange} />
                      )}
                    />
                    <Button type="button" variant="ghost" size="icon" onClick={() => setRemoveIndex(i)} aria-label="Remove field">
                      <Trash2 className="text-muted-foreground" />
                    </Button>
                  </div>
                )
              })}
            </div>
            {removedCount > 0 && (
              <p className="flex items-start gap-1.5 rounded-md bg-destructive/10 p-2 text-xs text-destructive">
                <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                Removing {removedCount} field{removedCount === 1 ? "" : "s"} deletes that data for every
                record in this collection when you save.
              </p>
            )}
          </div>

          {errors.fields && <p className="text-sm text-destructive">Check the field names above.</p>}
          {submitError && <p className="text-sm text-destructive">{submitError}</p>}

          <DialogFooter>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting && <Loader2 className="animate-spin" />}
              Save changes
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>

      <AlertDialog open={removeIndex !== null} onOpenChange={(next) => !next && setRemoveIndex(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove field {removeIndex !== null ? `"${fields[removeIndex]?.name || "(unnamed)"}"` : ""}?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently removes the field from the schema when you save, and deletes that data for
              every existing record in this collection. This can't be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => {
                if (removeIndex !== null) remove(removeIndex)
                setRemoveIndex(null)
              }}
            >
              Remove field
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <UnsavedChangesDialog
        open={confirmClose}
        onOpenChange={setConfirmClose}
        saving={isSubmitting}
        onDiscard={() => {
          setConfirmClose(false)
          setOpen(false)
        }}
        onSave={() => {
          setConfirmClose(false)
          handleSubmit(onSubmit)()
        }}
      />
    </Dialog>
  )
}
