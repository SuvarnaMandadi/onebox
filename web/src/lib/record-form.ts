// Converts between a record's real API value (what GET/PATCH/POST
// exchange — plain JS number/boolean/string/object) and the form-level
// value FieldInput edits (see its own doc comment for why json is edited
// as raw text). Centralized here so create/inline-edit/detail-sheet all
// convert identically.
import type { Field } from "@/lib/types"

export function apiToFormValue(field: Field, v: unknown): unknown {
  if (field.type === "json") {
    if (v === null || v === undefined) return ""
    return JSON.stringify(v, null, 2)
  }
  if (v === null || v === undefined) return field.type === "number" ? null : field.type === "bool" ? false : ""
  return v
}

/** Throws a human-readable Error if the form value can't convert (e.g. invalid JSON). */
export function formToApiValue(field: Field, v: unknown): unknown {
  if (field.type === "json") {
    const text = typeof v === "string" ? v.trim() : ""
    if (text === "") {
      if (field.required) throw new Error(`"${field.name}" must be valid JSON`)
      return null
    }
    try {
      return JSON.parse(text)
    } catch {
      throw new Error(`"${field.name}" must be valid JSON`)
    }
  }
  if (field.type === "text" || field.type === "date" || field.type === "relation") {
    if (v === "" || v === null || v === undefined) {
      if (field.required) throw new Error(`"${field.name}" is required`)
      return null
    }
    return v
  }
  if (field.type === "number") {
    if (v === "" || v === null || v === undefined) {
      if (field.required) throw new Error(`"${field.name}" is required`)
      return null
    }
    return typeof v === "number" ? v : Number(v)
  }
  return !!v
}
