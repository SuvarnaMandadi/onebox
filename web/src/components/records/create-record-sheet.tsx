import { useEffect, useState } from "react"
import { useForm, Controller } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { toast } from "sonner"
import { Loader2, Plus } from "lucide-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { FieldInput } from "@/components/records/field-input"
import { TypeBadge } from "@/components/records/field-cell"
import { buildRecordSchema } from "@/lib/record-zod"
import { defaultValueFor } from "@/lib/field-render"
import { formToApiValue } from "@/lib/record-form"
import { labelsFor, type RelationLabels } from "@/lib/relations"
import { api, ApiError } from "@/lib/api"
import type { Field, RecordRow } from "@/lib/types"

// open/onOpenChange are optional — same controllable-from-outside pattern
// as CreateCollectionDialog, so the command palette can open this
// directly after navigating to a collection's records page.
export function CreateRecordSheet({
  collectionName,
  fields,
  onCreated,
  open: openProp,
  onOpenChange: onOpenChangeProp,
  relationLabels,
}: {
  collectionName: string
  fields: Field[]
  onCreated: (r: RecordRow) => void
  open?: boolean
  onOpenChange?: (open: boolean) => void
  relationLabels?: RelationLabels
}) {
  const [internalOpen, setInternalOpen] = useState(false)
  const open = openProp ?? internalOpen
  const setOpen = onOpenChangeProp ?? setInternalOpen
  const [submitError, setSubmitError] = useState<string | null>(null)
  const schema = buildRecordSchema(fields)

  const {
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<Record<string, unknown>>({
    resolver: zodResolver(schema),
    defaultValues: Object.fromEntries(fields.map((f) => [f.name, defaultValueFor(f)])),
  })

  useEffect(() => {
    if (open) {
      reset(Object.fromEntries(fields.map((f) => [f.name, defaultValueFor(f)])))
      setSubmitError(null)
    }
  }, [open, fields, reset])

  async function onSubmit(values: Record<string, unknown>) {
    setSubmitError(null)
    let apiValues: Record<string, unknown>
    try {
      apiValues = Object.fromEntries(fields.map((f) => [f.name, formToApiValue(f, values[f.name])]))
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Invalid value")
      return
    }
    try {
      const rec = await api.post<RecordRow>(`/api/collections/${collectionName}/records`, apiValues)
      toast.success("Record created")
      onCreated(rec)
      setOpen(false)
    } catch (err) {
      setSubmitError(err instanceof ApiError ? err.message : "Failed to create record.")
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm">
          <Plus />
          New record
        </Button>
      </SheetTrigger>
      <SheetContent className="w-full overflow-y-auto sm:max-w-md">
        <SheetHeader>
          <SheetTitle>New record</SheetTitle>
          <SheetDescription>Added to "{collectionName}".</SheetDescription>
        </SheetHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4 px-4 pb-4">
          {fields.map((f) => (
            <div key={f.name} className="space-y-1.5">
              <Label htmlFor={`cr-${f.name}`} className="flex items-center gap-1.5 text-xs">
                {f.name}
                {f.required && <span className="text-destructive">*</span>}
                <TypeBadge type={f.type} />
              </Label>
              <Controller
                control={control}
                name={f.name}
                render={({ field }) => (
                  <FieldInput
                    id={`cr-${f.name}`}
                    field={f}
                    value={field.value}
                    onChange={field.onChange}
                    relationLabels={f.type === "relation" ? labelsFor(relationLabels ?? {}, f) : undefined}
                    ariaInvalid={!!errors[f.name]}
                  />
                )}
              />
              {errors[f.name] && (
                <p className="text-xs text-destructive">{String(errors[f.name]?.message ?? "Invalid value")}</p>
              )}
            </div>
          ))}
          {fields.length === 0 && (
            <p className="text-sm text-muted-foreground">This collection has no fields yet.</p>
          )}
          {submitError && <p className="text-sm text-destructive">{submitError}</p>}
          <SheetFooter className="px-0">
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting && <Loader2 className="animate-spin" />}
              Create record
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}
