import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { textRenderHint } from "@/lib/field-render"
import type { Field } from "@/lib/types"

// Shared editable control for one field — used by inline table-cell
// editing, the create-record panel, and the record detail sheet, so a
// field's editing behavior can't drift between the three surfaces. `value`
// is always the RHF-form-level representation: json fields are edited as
// their raw JSON text (parsed back to a real value on submit — see
// formToApiValue in record-form.ts), number fields use null for empty
// rather than NaN, everything else is its natural type. `id` is threaded
// through so a caller's <Label htmlFor> actually associates (accessibility,
// and lets tests/autofill target the control by field name via `name`).
export function FieldInput({
  field,
  value,
  onChange,
  autoFocus,
  className,
  onKeyDown,
  id,
  relationLabels,
  ariaInvalid,
}: {
  field: Field
  value: unknown
  onChange: (v: unknown) => void
  autoFocus?: boolean
  className?: string
  onKeyDown?: React.KeyboardEventHandler
  id?: string
  // id -> label map for a "relation" field's target collection (see
  // lib/relations.ts) — when present and non-empty, editing renders a
  // picker of real records instead of a bare text box for the id.
  relationLabels?: Map<string, string>
  // Drives the ui/input.tsx & ui/textarea.tsx aria-invalid:* styling —
  // undefined/false leaves the control's border untouched.
  ariaInvalid?: boolean
}) {
  const name = field.name
  switch (field.type) {
    case "relation": {
      const options = relationLabels ? Array.from(relationLabels.entries()) : []
      if (options.length === 0) {
        // No resolved options yet (still loading, or the target collection
        // has no records) — fall back to typing the id directly. The
        // backend still validates it references a real record on save.
        return (
          <Input
            id={id}
            name={name}
            placeholder={`record id in "${field.relation_collection}"`}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            autoFocus={autoFocus}
            onKeyDown={onKeyDown}
            className={className}
            aria-invalid={ariaInvalid}
          />
        )
      }
      return (
        <Select value={typeof value === "string" && value ? value : undefined} onValueChange={onChange}>
          <SelectTrigger id={id} className={className} aria-invalid={ariaInvalid}>
            <SelectValue placeholder={`Select a record in "${field.relation_collection}"…`} />
          </SelectTrigger>
          <SelectContent>
            {options.map(([recordId, label]) => (
              <SelectItem key={recordId} value={recordId}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )
    }
    case "bool":
      return (
        <Checkbox
          id={id}
          name={name}
          checked={!!value}
          onCheckedChange={onChange}
          autoFocus={autoFocus}
          className={className}
        />
      )
    case "number":
      return (
        <Input
          id={id}
          name={name}
          type="number"
          value={value === null || value === undefined ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))}
          autoFocus={autoFocus}
          onKeyDown={onKeyDown}
          className={className}
          aria-invalid={ariaInvalid}
        />
      )
    case "date":
      return (
        <Input
          id={id}
          name={name}
          type="date"
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value)}
          autoFocus={autoFocus}
          onKeyDown={onKeyDown}
          className={className}
          aria-invalid={ariaInvalid}
        />
      )
    case "json":
      return (
        <Textarea
          id={id}
          name={name}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value)}
          autoFocus={autoFocus}
          onKeyDown={onKeyDown}
          rows={4}
          className={`font-mono text-xs ${className ?? ""}`}
          aria-invalid={ariaInvalid}
        />
      )
    default: {
      const hint = textRenderHint(field.name)
      if (hint === "textarea") {
        return (
          <Textarea
            id={id}
            name={name}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            autoFocus={autoFocus}
            onKeyDown={onKeyDown}
            rows={3}
            className={className}
            aria-invalid={ariaInvalid}
          />
        )
      }
      return (
        <Input
          id={id}
          name={name}
          type={hint === "email" ? "email" : hint === "url" ? "url" : "text"}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value)}
          autoFocus={autoFocus}
          onKeyDown={onKeyDown}
          className={className}
          aria-invalid={ariaInvalid}
        />
      )
    }
  }
}
