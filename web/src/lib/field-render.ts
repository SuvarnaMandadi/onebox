// The schema engine has exactly 5 field types — text, number, bool, date,
// json (see validFieldTypes, internal/server/collection_schema.go) — no
// select/multi-select/email/url/image/file type exists at the backend
// level. Rather than fabricate schema concepts the API doesn't have,
// "type-aware rendering" here means: real backend types get a real
// dedicated control, and "text" gets a smarter presentation (email/url/
// long-text) chosen by a naming heuristic — cosmetic only, the stored
// value is still plain text either way. Anything else (a future field
// type this build doesn't recognize) falls back to a plain text input,
// same principle as capabilities.describe()'s "NOT supported yet" list.
import type { Field } from "@/lib/types"

export type RenderHint = "email" | "url" | "textarea" | "text"

const EMAIL_NAME_RE = /email/i
const URL_NAME_RE = /(^|_)(url|link|website|href)($|_)/i
const LONG_TEXT_NAME_RE = /(body|content|description|notes?|bio|summary|message|comment)/i

export function textRenderHint(fieldName: string): RenderHint {
  if (EMAIL_NAME_RE.test(fieldName)) return "email"
  if (URL_NAME_RE.test(fieldName)) return "url"
  if (LONG_TEXT_NAME_RE.test(fieldName)) return "textarea"
  return "text"
}

export function isLongTextValue(v: unknown): boolean {
  return typeof v === "string" && (v.length > 60 || v.includes("\n"))
}

export function defaultValueFor(field: Field): unknown {
  switch (field.type) {
    case "bool":
      return false
    case "number":
      return field.required ? 0 : null
    case "json":
      return field.required ? "{}" : null
    default:
      return field.required ? "" : null
  }
}
