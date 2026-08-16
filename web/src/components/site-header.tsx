import { Search } from "lucide-react"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Separator } from "@/components/ui/separator"
import { ThemeToggle } from "@/components/theme-toggle"
import { openCommandPalette } from "@/lib/command-palette-state"

const isMac = typeof navigator !== "undefined" && /Mac|iPhone/.test(navigator.platform)

export function SiteHeader({ title }: { title: string }) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-2 h-4" />
      <h1 className="text-sm font-medium">{title}</h1>
      <div className="ml-auto flex items-center gap-3">
        <button
          type="button"
          onClick={openCommandPalette}
          aria-label="Search"
          className="flex h-8 items-center gap-2 rounded-md border bg-background px-2.5 text-sm text-muted-foreground transition-colors hover:bg-accent sm:w-56"
        >
          <Search className="size-3.5" />
          <span className="hidden flex-1 text-left sm:inline">Search…</span>
          <kbd className="hidden rounded border bg-muted px-1.5 py-0.5 text-[10px] font-medium sm:inline-block">{isMac ? "⌘K" : "Ctrl K"}</kbd>
        </button>
        <ThemeToggle />
      </div>
    </header>
  )
}
