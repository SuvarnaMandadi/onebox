import { useState, type ComponentPropsWithoutRef } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkMath from "remark-math"
import rehypeKatex from "rehype-katex"
import rehypeHighlight from "rehype-highlight"
import { Check, Copy } from "lucide-react"
import { MermaidDiagram } from "@/components/ai/mermaid-diagram"
import { cn } from "@/lib/utils"
import "katex/dist/katex.min.css"
import "highlight.js/styles/github-dark.css"

function CodeBlock({ className, children }: ComponentPropsWithoutRef<"code">) {
  const [copied, setCopied] = useState(false)
  const lang = /language-(\w+)/.exec(className || "")?.[1]
  const text = String(children).replace(/\n$/, "")

  if (lang === "mermaid") return <MermaidDiagram code={text} />

  return (
    <div className="group/code relative my-2">
      {lang && <span className="absolute right-10 top-1.5 text-[10px] uppercase text-zinc-400">{lang}</span>}
      <button
        type="button"
        onClick={() => {
          navigator.clipboard.writeText(text)
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        }}
        className="absolute right-1.5 top-1.5 rounded p-1 text-zinc-400 opacity-0 transition-opacity hover:bg-zinc-700 hover:text-zinc-100 group-hover/code:opacity-100"
        aria-label="Copy code"
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
      <pre className={cn(className, "overflow-x-auto rounded-md bg-zinc-900 p-3 text-xs")}>
        <code className={className}>{children}</code>
      </pre>
    </div>
  )
}

export function MarkdownRenderer({ content, className }: { content: string; className?: string }) {
  return (
    <div className={cn("prose prose-sm dark:prose-invert max-w-none break-words", "prose-pre:bg-transparent prose-pre:p-0", className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        // rehype-raw (which parses raw HTML embedded in the markdown
        // source into the render tree) is deliberately NOT included here:
        // this renders LLM output and, per the coexist test, potentially
        // echoed user text, and rehype-raw with no sanitize pass is a
        // known XSS vector (react-markdown's own docs warn against using
        // it unsanitized). KaTeX and syntax highlighting below don't need
        // it — both operate on the markdown AST directly, not on raw HTML
        // strings. Any literal HTML the model emits now renders as inert
        // escaped text instead of live markup, matching how other AI chat
        // UIs treat model output by default.
        rehypePlugins={[rehypeKatex, rehypeHighlight]}
        components={{
          code({ className, children, ...props }) {
            const isBlock = /language-/.test(className || "") || String(children).includes("\n")
            if (!isBlock) {
              return (
                <code className="rounded bg-muted px-1 py-0.5 text-[0.85em]" {...props}>
                  {children}
                </code>
              )
            }
            return (
              <CodeBlock className={className} {...props}>
                {children}
              </CodeBlock>
            )
          },
          pre({ children }) {
            return <>{children}</>
          },
          a({ href, children, ...props }) {
            return (
              <a href={href} target="_blank" rel="noreferrer" {...props}>
                {children}
              </a>
            )
          },
          img({ src, alt }) {
            // eslint-disable-next-line @next/next/no-img-element -- not a Next.js app
            return <img src={src} alt={alt} className="max-h-80 rounded-md border" loading="lazy" />
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
}
