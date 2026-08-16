import { useEffect, useState } from "react"
import { AlertTriangle, Loader2 } from "lucide-react"
import { fetchBlob, ApiError } from "@/lib/api"
import { previewKind } from "@/lib/file-render"
import type { FileRecord } from "@/lib/types"

// Fetches and renders a file's content only once actually mounted (the
// caller — FileDetailSheet — only mounts this when its sheet is open), so
// browsing a grid of 100 files never downloads 100 files' bytes just to
// show icons.
export function FilePreview({ file }: { file: FileRecord }) {
  const kind = previewKind(file.mime, file.filename)
  const [state, setState] = useState<{ loading: boolean; error: string | null; url?: string; text?: string }>({
    loading: true,
    error: null,
  })

  useEffect(() => {
    if (kind === "none") {
      setState({ loading: false, error: null })
      return
    }
    let objectUrl: string | undefined
    // If `file` changes again before this fetch resolves (rapidly paging
    // between files in a detail sheet), the effect cleanup below flips
    // this so the stale response's .then/.catch is a no-op instead of
    // overwriting state with content for a file that's no longer shown.
    let ignore = false
    setState({ loading: true, error: null })
    fetchBlob(`/api/files/${file.id}`)
      .then(async (blob) => {
        if (ignore) return
        if (kind === "image" || kind === "pdf") {
          objectUrl = URL.createObjectURL(blob)
          setState({ loading: false, error: null, url: objectUrl })
        } else {
          const text = await blob.text()
          if (ignore) return
          setState({ loading: false, error: null, text })
        }
      })
      .catch((err) => {
        if (ignore) return
        setState({ loading: false, error: err instanceof ApiError ? err.message : "Failed to load preview" })
      })
    return () => {
      ignore = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [file.id, kind])

  if (kind === "none") {
    return (
      <div className="flex flex-col items-center gap-2 rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground">
        <p>No inline preview for this file type.</p>
        <p className="text-xs">Download it to view the contents.</p>
      </div>
    )
  }

  if (state.loading) {
    return (
      <div className="flex items-center justify-center rounded-md border p-8">
        <Loader2 className="size-5 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (state.error) {
    return (
      <div className="flex items-center gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
        <AlertTriangle className="size-4 shrink-0" />
        {state.error}
      </div>
    )
  }

  if (kind === "image") {
    return <img src={state.url} alt={file.filename} className="max-h-96 w-full rounded-md border object-contain" />
  }
  if (kind === "pdf") {
    // sandbox="" (no allow-scripts/allow-same-origin/etc.) — an uploaded
    // PDF is untrusted content; the browser's built-in PDF renderer still
    // works inside a fully-restrictive sandbox, but any script a
    // maliciously-crafted PDF tried to run would be blocked.
    return <iframe src={state.url} title={file.filename} sandbox="" className="h-96 w-full rounded-md border" />
  }
  if (kind === "json") {
    let pretty = state.text ?? ""
    try {
      pretty = JSON.stringify(JSON.parse(state.text ?? ""), null, 2)
    } catch {
      /* not valid JSON — show raw text instead */
    }
    return <pre className="max-h-96 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs">{pretty}</pre>
  }
  if (kind === "csv") {
    return <CsvPreview text={state.text ?? ""} />
  }
  // markdown and plain text — shown as real source, not fake-rendered HTML
  // (no markdown-to-HTML dependency added for this milestone).
  return <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-xs">{state.text}</pre>
}

function CsvPreview({ text }: { text: string }) {
  const rows = text
    .split(/\r?\n/)
    .filter((r) => r.trim() !== "")
    .slice(0, 50)
    .map((r) => r.split(","))
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">Empty file.</p>
  return (
    <div className="max-h-96 overflow-auto rounded-md border">
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-muted">
          <tr>
            {rows[0].map((cell, i) => (
              <th key={i} className="border-b px-2 py-1 text-left font-medium">
                {cell}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.slice(1).map((row, i) => (
            <tr key={i} className="border-b last:border-b-0">
              {row.map((cell, j) => (
                <td key={j} className="px-2 py-1">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
