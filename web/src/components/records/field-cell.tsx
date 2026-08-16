import { Link } from "react-router-dom"
import { Check, X } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { HighlightText } from "@/components/highlight-text"
import { textRenderHint } from "@/lib/field-render"
import type { Field } from "@/lib/types"

export function FieldCell({
  field,
  value,
  query,
  relationLabel,
}: {
  field: Field
  value: unknown
  query?: string
  // The resolved display label for a "relation" field's value (its target
  // record's first text field — see lib/relations.ts). Undefined for every
  // non-relation field, and falls back to the raw id if the lookup hasn't
  // resolved yet.
  relationLabel?: string
}) {
  if (value === null || value === undefined || value === "") {
    return <span className="text-muted-foreground">—</span>
  }

  switch (field.type) {
    case "relation": {
      const id = String(value)
      return (
        <Link
          to={`/collections/${field.relation_collection}/records?open=${encodeURIComponent(id)}`}
          onClick={(e) => e.stopPropagation()}
          className="text-primary underline underline-offset-2"
          title={`Open in ${field.relation_collection}`}
        >
          {relationLabel ?? id}
        </Link>
      )
    }
    case "bool":
      return value ? (
        <Check className="size-4 text-green-600 dark:text-green-500" />
      ) : (
        <X className="size-4 text-muted-foreground" />
      )
    case "number":
      return <span className="tabular-nums">{String(value)}</span>
    case "date":
      return <span>{String(value)}</span>
    case "json":
      return (
        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
          {JSON.stringify(value).slice(0, 60)}
        </code>
      )
    default: {
      const text = String(value)
      const hint = textRenderHint(field.name)
      if (hint === "url") {
        return (
          <a
            href={text}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            className="text-primary underline underline-offset-2"
          >
            {text}
          </a>
        )
      }
      if (hint === "email") {
        return (
          <a href={`mailto:${text}`} onClick={(e) => e.stopPropagation()} className="text-primary underline underline-offset-2">
            {text}
          </a>
        )
      }
      return (
        <span className="line-clamp-2">
          <HighlightText text={text} query={query} />
        </span>
      )
    }
  }
}

export function TypeBadge({ type }: { type: string }) {
  return (
    <Badge variant="outline" className="font-normal text-muted-foreground">
      {type}
    </Badge>
  )
}
