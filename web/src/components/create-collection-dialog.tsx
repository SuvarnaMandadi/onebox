import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useForm, useFieldArray, Controller } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { toast } from "sonner"
import { Loader2, Plus, Trash2 } from "lucide-react"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { IconColorPicker } from "@/components/icon-color-picker"
import { UnsavedChangesDialog } from "@/components/unsaved-changes-dialog"
import { FieldValidationPopover } from "@/components/field-validation-popover"
import { COLLECTION_TEMPLATES } from "@/lib/collection-templates"
import { setMeta } from "@/lib/collection-meta"
import { useCollections } from "@/lib/collections-store"
import { validateFields } from "@/lib/field-validation"
import { FIELD_TYPES } from "@/lib/field-types"
import { api, ApiError } from "@/lib/api"
import type { Field } from "@/lib/types"

const NAME_RE = /^[a-zA-Z][a-zA-Z0-9_]{0,62}$/

// Field names still need to already be a legal identifier — unlike the
// collection name below, nothing on the backend slugifies a field name.
const fieldValidationSchema = z
  .object({
    format: z.enum(["email", "url"]).optional(),
    min_length: z.number().optional(),
    max_length: z.number().optional(),
    pattern: z.string().optional(),
    min: z.number().optional(),
    max: z.number().optional(),
    unique: z.boolean().optional(),
  })
  .optional()

const fieldSchema = z.object({
  name: z.string().min(1, "Required").regex(NAME_RE, "Letters/digits/underscore, must start with a letter"),
  type: z.enum(FIELD_TYPES),
  required: z.boolean(),
  relation_collection: z.string().optional(),
  validation: fieldValidationSchema,
})

// Just needs a letter somewhere — createCollection slugifies anything
// else (spaces, punctuation) into a legal identifier server-side (see
// SlugifyCollectionName / previewSlug below), so "Customer Details" must
// be allowed to submit here rather than rejected before the backend ever
// sees it (RC2: "do not force users to manually remove spaces").
const COLLECTION_NAME_RE = /[a-zA-Z]/

const formSchema = z.object({
  name: z
    .string()
    .min(1, "Required")
    .regex(COLLECTION_NAME_RE, "Must contain at least one letter"),
  description: z.string().max(280, "Keep it under 280 characters").optional(),
  icon: z.string(),
  color: z.string(),
  fields: z.array(fieldSchema).min(1, "Add at least one field — a collection needs at least one"),
})

// previewSlug mirrors SlugifyCollectionName (internal/server/collection_schema.go)
// closely enough for a live "will be created as" preview — cosmetic only,
// the backend's own transform is what's actually authoritative and always
// gets the final say on the real stored name.
function previewSlug(name: string): string {
  if (NAME_RE.test(name)) return name
  const slug = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 63)
  return slug || name
}

type FormValues = z.infer<typeof formSchema>

// open/onOpenChange are optional so the command palette's "Create
// collection" action can drive this dialog externally (navigate here,
// then open it) while every other caller keeps the simple
// trigger-button-owns-its-own-state behavior.
export function CreateCollectionDialog({
  open: openProp,
  onOpenChange: onOpenChangeProp,
}: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const [internalOpen, setInternalOpen] = useState(false)
  const open = openProp ?? internalOpen
  const setOpen = onOpenChangeProp ?? setInternalOpen
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [confirmClose, setConfirmClose] = useState(false)
  const { collections, refresh } = useCollections()
  const navigate = useNavigate()

  const {
    register,
    handleSubmit,
    control,
    reset,
    watch,
    setValue,
    formState: { errors, isSubmitting, isDirty },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: { name: "", description: "", icon: "Database", color: "gray", fields: [] },
  })

  function closeAndReset() {
    setOpen(false)
    reset()
    setSubmitError(null)
  }

  // RC2: don't silently discard a half-filled "New collection" form on
  // close — same UnsavedChangesDialog EditCollectionDialog uses.
  function requestClose() {
    if (isDirty) {
      setConfirmClose(true)
      return
    }
    closeAndReset()
  }

  const { fields, append, remove } = useFieldArray({ control, name: "fields" })
  const icon = watch("icon")
  const color = watch("color")
  const nameValue = watch("name")
  const slugPreview = nameValue ? previewSlug(nameValue) : ""

  function applyTemplate(templateId: string) {
    const tpl = COLLECTION_TEMPLATES.find((t) => t.id === templateId)
    if (!tpl) return
    setValue("icon", tpl.icon)
    setValue("color", tpl.color)
    setValue("fields", tpl.fields as Field[])
  }

  async function onSubmit(values: FormValues) {
    setSubmitError(null)
    const fieldsError = validateFields(values.fields)
    if (fieldsError) {
      setSubmitError(fieldsError)
      return
    }
    try {
      // The name actually stored can differ from what was typed — spaces/
      // punctuation get slugified into a legal identifier server-side
      // (SlugifyCollectionName) — so every post-create step below uses
      // the server's real created.name, never the raw values.name, or a
      // "Customer Details" submission would set metadata on a name that
      // was never created and 404 trying to navigate to it.
      const created = await api.post<{ name: string }>("/api/collections", {
        name: values.name,
        schema: { fields: values.fields },
        rules: {},
      })
      setMeta(created.name, {
        icon: values.icon,
        color: values.color,
        description: values.description ?? "",
      })
      await refresh()
      toast.success(`Collection "${created.name}" created`)
      setOpen(false)
      reset()
      navigate(`/collections/${created.name}`)
    } catch (err) {
      setSubmitError(err instanceof ApiError ? err.message : "Failed to create collection.")
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setOpen(true)
        } else {
          requestClose()
        }
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus />
          New collection
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>New collection</DialogTitle>
          <DialogDescription>
            A collection is a schema-defined table with a REST API and realtime subscriptions built in.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-5">
          <div className="space-y-2">
            <Label>Template</Label>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              {COLLECTION_TEMPLATES.map((tpl) => (
                <button
                  key={tpl.id}
                  type="button"
                  onClick={() => applyTemplate(tpl.id)}
                  className="rounded-md border px-3 py-2 text-left text-xs transition-colors hover:bg-accent"
                >
                  <p className="font-medium">{tpl.label}</p>
                  <p className="text-muted-foreground">{tpl.description}</p>
                </button>
              ))}
            </div>
          </div>

          <div className="flex gap-3">
            <IconColorPicker
              icon={icon}
              color={color}
              onIconChange={(v) => setValue("icon", v)}
              onColorChange={(v) => setValue("color", v)}
            />
            <div className="flex-1 space-y-2">
              <Label htmlFor="cc-name">Name</Label>
              <Input id="cc-name" placeholder="e.g. tasks" {...register("name")} />
              {errors.name ? (
                <p className="text-xs text-destructive">{errors.name.message}</p>
              ) : (
                slugPreview && slugPreview !== nameValue && (
                  <p className="text-xs text-muted-foreground">
                    Will be created as <code className="font-mono">{slugPreview}</code>
                  </p>
                )
              )}
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="cc-description">Description</Label>
            <Textarea id="cc-description" placeholder="What's this collection for?" rows={2} {...register("description")} />
            {errors.description && <p className="text-xs text-destructive">{errors.description.message}</p>}
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>Fields</Label>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => append({ name: "", type: "text", required: false })}
              >
                <Plus />
                Add field
              </Button>
            </div>
            {fields.length === 0 && (
              <p className="rounded-md border border-dashed p-3 text-center text-xs text-muted-foreground">
                Add at least one field — id/created/updated/owner_id are added automatically on top of these.
              </p>
            )}
            {errors.fields?.root && <p className="text-xs text-destructive">{errors.fields.root.message}</p>}
            <div className="space-y-2">
              {fields.map((f, i) => {
                const fieldType = watch(`fields.${i}.type`)
                return (
                  <div key={f.id} className="flex flex-wrap items-start gap-2">
                    <div className="flex-1 basis-32">
                      <Input placeholder="field_name" {...register(`fields.${i}.name` as const)} />
                      {errors.fields?.[i]?.name && (
                        <p className="mt-1 text-xs text-destructive">{errors.fields[i]?.name?.message}</p>
                      )}
                    </div>
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
                      <div>
                        <Controller
                          control={control}
                          name={`fields.${i}.relation_collection` as const}
                          render={({ field }) => (
                            <Select value={field.value ?? ""} onValueChange={field.onChange}>
                              <SelectTrigger className="w-36">
                                <SelectValue placeholder="Target collection" />
                              </SelectTrigger>
                              <SelectContent>
                                {(collections ?? []).map((c) => (
                                  <SelectItem key={c.name} value={c.name}>
                                    {c.name}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          )}
                        />
                        {errors.fields?.[i]?.relation_collection && (
                          <p className="mt-1 text-xs text-destructive">
                            {errors.fields[i]?.relation_collection?.message}
                          </p>
                        )}
                      </div>
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
                    <Button type="button" variant="ghost" size="icon" onClick={() => remove(i)} aria-label="Remove field">
                      <Trash2 className="text-muted-foreground" />
                    </Button>
                  </div>
                )
              })}
            </div>
          </div>

          {submitError && <p className="text-sm text-destructive">{submitError}</p>}

          <DialogFooter>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting && <Loader2 className="animate-spin" />}
              Create collection
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>

      <UnsavedChangesDialog
        open={confirmClose}
        onOpenChange={setConfirmClose}
        saving={isSubmitting}
        onDiscard={() => {
          setConfirmClose(false)
          closeAndReset()
        }}
        onSave={() => {
          setConfirmClose(false)
          handleSubmit(onSubmit)()
        }}
      />
    </Dialog>
  )
}
