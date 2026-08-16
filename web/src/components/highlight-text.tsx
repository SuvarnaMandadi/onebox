// Shared substring highlighter — same visual treatment already used
// ad-hoc in the Records and Files pages, centralized here for the command
// palette so a third near-duplicate isn't hand-rolled again.
export function HighlightText({ text, query }: { text: string; query?: string }) {
  if (!query?.trim()) return <>{text}</>
  const i = text.toLowerCase().indexOf(query.trim().toLowerCase())
  if (i === -1) return <>{text}</>
  return (
    <>
      {text.slice(0, i)}
      <mark className="rounded-sm bg-amber-200 px-0.5 text-inherit dark:bg-amber-500/40">
        {text.slice(i, i + query.trim().length)}
      </mark>
      {text.slice(i + query.trim().length)}
    </>
  )
}
