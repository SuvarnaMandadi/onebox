// Resolves relation field values (record ids) into a human-readable
// display label, and backs the AI Workspace's understanding of what a
// relation link means — the frontend counterpart to Milestone 6's backend
// relation model (Field.relation_collection, find_related_records).
//
// Label resolution reuses the exact "first text-type field as label"
// heuristic command-palette.tsx already established for @mention record
// suggestions (see recordLabelField there — picking the first schema field
// of type "text" rather than guessing from a record's raw JSON shape,
// which command-palette.tsx's own comment documents as a real bug fixed in
// Milestone 4). Falls back to the raw record id when the target collection
// has no text field, or hasn't loaded yet.
import { useEffect, useState } from "react"
import { api } from "@/lib/api"
import type { Collection, Field, RecordsListResponse } from "@/lib/types"

// Records fetched per target collection to build its id -> label map.
// Same "brute-force is fine at this project's scale" tradeoff already made
// elsewhere (e.g. countCollectionRecords's plain COUNT(*), records-page.tsx's
// own limit=200 initial load) — a proper "look up just these N ids"
// endpoint would be the right answer well past this scale, not yet.
const RELATION_FETCH_LIMIT = 200

export type RelationLabels = Record<string, Map<string, string>>

function firstTextField(collections: Collection[] | null, name: string): string | undefined {
  return collections?.find((c) => c.name === name)?.schema.fields.find((f) => f.type === "text")?.name
}

/**
 * Given a collection's field list, fetches (once per distinct target,
 * cheaply re-fetched on remount — no cross-page cache) every relation
 * target collection's records and returns { [targetCollectionName]:
 * Map<recordId, label> }. A field with no relation fields returns `{}`
 * immediately with no request made.
 */
export function useRelationLabels(fields: Field[], collections: Collection[] | null): RelationLabels {
  const targets = Array.from(
    new Set(
      fields.filter((f) => f.type === "relation" && f.relation_collection).map((f) => f.relation_collection as string),
    ),
  ).sort()
  const key = targets.join(",")

  const [labels, setLabels] = useState<RelationLabels>({})

  useEffect(() => {
    if (targets.length === 0) {
      setLabels({})
      return
    }
    let cancelled = false
    Promise.all(
      targets.map(async (name): Promise<[string, Map<string, string>]> => {
        try {
          const res = await api.get<RecordsListResponse>(
            `/api/collections/${name}/records?limit=${RELATION_FETCH_LIMIT}`,
          )
          const field = firstTextField(collections, name)
          const map = new Map<string, string>()
          for (const r of res.items) {
            const v = field ? r[field] : undefined
            map.set(r.id, typeof v === "string" && v ? v : r.id)
          }
          return [name, map]
        } catch {
          // Best-effort only — a failed lookup (deleted collection,
          // permissions) just falls back to showing raw ids, never blocks
          // rendering the rest of the table/sheet.
          return [name, new Map<string, string>()]
        }
      }),
    ).then((entries) => {
      if (!cancelled) setLabels(Object.fromEntries(entries))
    })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return labels
}

/** The Map for one specific relation field, or undefined if it isn't a relation field / hasn't resolved yet. */
export function labelsFor(relationLabels: RelationLabels, field: Field): Map<string, string> | undefined {
  if (field.type !== "relation" || !field.relation_collection) return undefined
  return relationLabels[field.relation_collection]
}
