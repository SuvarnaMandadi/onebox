import { z } from "zod"
import type { Field } from "@/lib/types"

// Builds a Zod object schema from a collection's live field list, operating
// on FieldInput's form-level representation (see record-form.ts) — used by
// CreateRecordSheet so react-hook-form gets real per-field validation
// without hand-writing a schema per collection.
export function buildRecordSchema(fields: Field[]) {
  const shape: Record<string, z.ZodTypeAny> = {}
  for (const f of fields) {
    switch (f.type) {
      case "bool":
        shape[f.name] = z.boolean()
        break
      case "number":
        shape[f.name] = f.required
          ? z.number({ error: `"${f.name}" is required` })
          : z.number().nullable()
        break
      default:
        // text, date, json all edit as strings at the form level (json's
        // JSON-parse validity is checked at submit time — see
        // formToApiValue — since a schema-level regex can't validate JSON).
        shape[f.name] = f.required
          ? z.string().min(1, `"${f.name}" is required`)
          : z.string().nullable().optional()
    }
  }
  return z.object(shape)
}
