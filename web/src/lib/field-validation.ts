import type { Field } from "@/lib/types"

// Mirrors ValidateSchema's non-empty/reserved/duplicate checks
// (internal/server/collection_schema.go) so obviously-invalid field lists
// are caught before a round trip to the API, not after.
export const RESERVED_FIELD_NAMES = new Set(["id", "owner_id", "created", "updated"])

export function validateFields(fields: Field[]): string | null {
  if (fields.length === 0) return "A collection needs at least one field."
  const seen = new Set<string>()
  for (const f of fields) {
    if (RESERVED_FIELD_NAMES.has(f.name)) return `Field name "${f.name}" is reserved.`
    if (seen.has(f.name)) return `Duplicate field name "${f.name}".`
    seen.add(f.name)
    // A "relation" field with no target collection selected can't render
    // or resolve anything — field-input.tsx has no target to look up
    // options from, and shows a broken "record id in undefined" prompt
    // for every record. Catch it here instead of letting it reach the API.
    if (f.type === "relation" && !f.relation_collection) {
      return `Field "${f.name}" needs a target collection to relate to.`
    }
  }
  return null
}
