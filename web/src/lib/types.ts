// FieldValidation mirrors FieldValidation (internal/server/collection_schema.go)
// field-for-field. Every property is type-scoped — format/min_length/
// max_length/pattern only apply to "text" fields, min/max only to
// "number" — except unique, which applies to any type. See
// field-validation-popover.tsx, which is the only place that builds one of
// these from form input, for why the UI never lets an admin set a
// rule that doesn't apply to the field's own type.
export interface FieldValidation {
  format?: "email" | "url"
  min_length?: number
  max_length?: number
  pattern?: string
  min?: number
  max?: number
  unique?: boolean
}

export interface Field {
  name: string
  type: "text" | "number" | "bool" | "date" | "json" | "relation"
  required: boolean
  // relation_collection names the target collection a "relation"-typed
  // field points at — the field's value is that collection's record id.
  // Undefined/empty for every other field type. Mirrors Field.RelationCollection
  // (internal/server/collection_schema.go).
  relation_collection?: string
  // validation holds this field's optional, type-appropriate rules (RC4:
  // previously only settable via the AI assistant or a raw API call —
  // never exposed in either dashboard form). Undefined means "no extra
  // rules beyond required."
  validation?: FieldValidation
}

export interface Schema {
  fields: Field[]
}

export interface Collection {
  id: string
  name: string
  schema: Schema
  record_count: number
  created: string
  updated: string
}

// RecordRow is one row from GET /api/collections/:name/records — the
// backend returns a plain map[string]any (see records.go), so the
// schema-defined fields ride alongside the fixed id/created/updated/
// owner_id columns with no separate envelope.
export interface RecordRow {
  id: string
  created: string
  updated: string
  owner_id?: string | null
  [key: string]: unknown
}

export interface RecordsListResponse {
  items: RecordRow[]
  nextCursor?: string
}

// FileRecord mirrors fileRecord (internal/server/files.go) — flat, no
// folder hierarchy, no "updated" column (files are immutable blobs; only
// their metadata row is ever touched, and only at create/delete).
export interface FileRecord {
  id: string
  owner_id?: string
  filename: string
  size: number
  mime: string
  created: string
}

export interface FilesListResponse {
  items: FileRecord[]
  nextCursor?: string
  total: number
}

export interface LogEntry {
  id: string
  time: string
  method: string
  path: string
  status: number
  user_id?: string
  duration_ms: number
}

export interface ChatbotResponse {
  reply: string
  actions?: Array<{ type: string; title: string }>
}
