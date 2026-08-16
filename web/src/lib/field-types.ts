// The schema engine's field types (mirrors validFieldTypes,
// internal/server/collection_schema.go) — single source of truth so
// create-collection-dialog.tsx and edit-collection-dialog.tsx (both of
// which render a field-type <Select>) can't drift apart on which types
// exist. "relation" was added in Milestone 6.
export const FIELD_TYPES = ["text", "number", "bool", "date", "json", "relation"] as const
