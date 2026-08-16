import { SlidersHorizontal } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import type { Field, FieldValidation } from "@/lib/types"

// FieldValidationPopover is the RC4 addition that closes a real gap: the
// backend has supported per-field validation (unique/format/length/
// pattern/range) since RC2, and the AI assistant has been able to propose
// it since RC3, but neither the create nor edit collection dialog exposed
// any control for it at all — an admin filling in the form by hand had no
// way to set it without asking the AI or calling the API directly. Shared
// by create-collection-dialog.tsx and edit-collection-dialog.tsx rather
// than duplicated, since it's the exact same widget in both places.
//
// Deliberately scoped to the same rule set the AI tool schema already
// offers (validationSchemaProp, chatbot_actions.go) — format/min_length/
// max_length/pattern/min/max/unique — not the full FieldValidation struct
// (FutureOnly/PastOnly/Default also exist server-side but aren't wired up
// on either authoring path yet; see RC4's report for that as a follow-up,
// not silently expanding this component's scope beyond what's consistent
// today).
export function FieldValidationPopover({
  type,
  value,
  onChange,
}: {
  type: Field["type"]
  value: FieldValidation | undefined
  onChange: (next: FieldValidation | undefined) => void
}) {
  const v = value ?? {}
  const isText = type === "text"
  const isNumber = type === "number"
  const hasRules = Object.keys(v).some((k) => v[k as keyof FieldValidation] !== undefined && v[k as keyof FieldValidation] !== "")

  function set(patch: Partial<FieldValidation>) {
    const next: FieldValidation = { ...v, ...patch }
    // Drop empty/undefined keys rather than persisting "format: ''" —
    // keeps the payload identical to a field nobody touched, and keeps
    // hasRules accurate.
    for (const k of Object.keys(next) as (keyof FieldValidation)[]) {
      if (next[k] === undefined || next[k] === "" || next[k] === false) delete next[k]
    }
    onChange(Object.keys(next).length > 0 ? next : undefined)
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant={hasRules ? "secondary" : "ghost"}
          size="icon"
          aria-label="Field validation"
          title="Validation rules"
        >
          <SlidersHorizontal className={hasRules ? "text-foreground" : "text-muted-foreground"} />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-64 space-y-3" align="start">
        <p className="text-xs font-medium">Validation</p>

        {isText && (
          <>
            <div className="space-y-1.5">
              <Label className="text-xs">Format</Label>
              <Select
                value={v.format ?? "none"}
                onValueChange={(next) => set({ format: next === "none" ? undefined : (next as FieldValidation["format"]) })}
              >
                <SelectTrigger className="h-8 w-full text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">None</SelectItem>
                  <SelectItem value="email">Email</SelectItem>
                  <SelectItem value="url">URL</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex gap-2">
              <div className="flex-1 space-y-1.5">
                <Label className="text-xs">Min length</Label>
                <Input
                  type="number"
                  className="h-8 text-xs"
                  value={v.min_length ?? ""}
                  onChange={(e) => set({ min_length: e.target.value === "" ? undefined : Number(e.target.value) })}
                />
              </div>
              <div className="flex-1 space-y-1.5">
                <Label className="text-xs">Max length</Label>
                <Input
                  type="number"
                  className="h-8 text-xs"
                  value={v.max_length ?? ""}
                  onChange={(e) => set({ max_length: e.target.value === "" ? undefined : Number(e.target.value) })}
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Pattern (regex)</Label>
              <Input
                className="h-8 font-mono text-xs"
                placeholder="e.g. [A-Z]{3}-[0-9]+"
                value={v.pattern ?? ""}
                onChange={(e) => set({ pattern: e.target.value })}
              />
            </div>
          </>
        )}

        {isNumber && (
          <div className="flex gap-2">
            <div className="flex-1 space-y-1.5">
              <Label className="text-xs">Min</Label>
              <Input
                type="number"
                className="h-8 text-xs"
                value={v.min ?? ""}
                onChange={(e) => set({ min: e.target.value === "" ? undefined : Number(e.target.value) })}
              />
            </div>
            <div className="flex-1 space-y-1.5">
              <Label className="text-xs">Max</Label>
              <Input
                type="number"
                className="h-8 text-xs"
                value={v.max ?? ""}
                onChange={(e) => set({ max: e.target.value === "" ? undefined : Number(e.target.value) })}
              />
            </div>
          </div>
        )}

        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Checkbox checked={!!v.unique} onCheckedChange={(next) => set({ unique: next === true })} />
          Unique — no other record may share this value
        </label>
      </PopoverContent>
    </Popover>
  )
}
