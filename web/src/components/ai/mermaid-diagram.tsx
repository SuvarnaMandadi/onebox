import { useEffect, useId, useState } from "react"

// Mermaid (~500KB) is dynamically imported only when a reply actually
// contains a ```mermaid block, instead of being in the main bundle for
// every page — see MarkdownRenderer's code-block handling.
export function MermaidDiagram({ code }: { code: string }) {
  const id = useId().replace(/:/g, "-")
  const [svg, setSvg] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    import("mermaid").then(async (mod) => {
      const mermaid = mod.default
      mermaid.initialize({ startOnLoad: false, theme: "neutral", securityLevel: "strict" })
      try {
        const { svg } = await mermaid.render(`mermaid-${id}`, code)
        if (!cancelled) setSvg(svg)
      } catch {
        if (!cancelled) setError("Couldn't render this diagram.")
      }
    })
    return () => {
      cancelled = true
    }
  }, [code, id])

  if (error) {
    return <pre className="overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs">{code}</pre>
  }
  if (!svg) {
    return <div className="h-24 animate-pulse rounded-md border bg-muted/40" />
  }
  // eslint-disable-next-line react/no-danger -- mermaid.render output, securityLevel "strict" sanitizes it
  return <div className="overflow-auto rounded-md border bg-card p-3 [&_svg]:mx-auto" dangerouslySetInnerHTML={{ __html: svg }} />
}
