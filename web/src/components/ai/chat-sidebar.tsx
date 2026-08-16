import { useMemo, useState } from "react"
import { toast } from "sonner"
import {
  Archive,
  ArchiveRestore,
  Copy,
  Download,
  MoreHorizontal,
  Pencil,
  Pin,
  PlusCircle,
  Search,
  Star,
  Trash2,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  deleteConversation,
  duplicateConversation,
  exportJSON,
  exportMarkdown,
  renameConversation,
  toggleArchive,
  toggleFavorite,
  togglePin,
  useConversations,
  type AiConversation,
} from "@/lib/ai-conversations"
import { formatRelativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

function download(filename: string, content: string, mime: string) {
  const blob = new Blob([content], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export function ChatSidebar({
  activeId,
  onSelect,
  onNew,
}: {
  activeId: string | null
  onSelect: (id: string) => void
  onNew: () => void
}) {
  const all = useConversations()
  const [query, setQuery] = useState("")
  const [showArchived, setShowArchived] = useState(false)
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState("")
  const [deleteTarget, setDeleteTarget] = useState<AiConversation | null>(null)

  const filtered = useMemo(() => {
    let list = all.filter((c) => (showArchived ? true : !c.archived))
    if (query.trim()) {
      const q = query.trim().toLowerCase()
      list = list.filter((c) => c.title.toLowerCase().includes(q) || c.messages.some((m) => m.content.toLowerCase().includes(q)))
    }
    return list
  }, [all, query, showArchived])

  const pinned = filtered.filter((c) => c.pinned)
  const favorites = filtered.filter((c) => !c.pinned && c.favorite)
  const recent = filtered.filter((c) => !c.pinned && !c.favorite)

  return (
    <div className="flex h-full flex-col">
      <div className="space-y-2 border-b p-3">
        <Button size="sm" className="w-full justify-start" onClick={onNew}>
          <PlusCircle />
          New chat
        </Button>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input placeholder="Search conversations…" className="h-8 pl-8" value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
      </div>
      <div className="flex-1 overflow-y-auto p-2">
        {filtered.length === 0 && (
          <p className="p-4 text-center text-xs text-muted-foreground">
            {query ? "No conversations match." : "No conversations yet — start one above."}
          </p>
        )}
        <Section title="Pinned" items={pinned} activeId={activeId} onSelect={onSelect} {...actionProps()} />
        <Section title="Favorites" items={favorites} activeId={activeId} onSelect={onSelect} {...actionProps()} />
        <Section title={pinned.length || favorites.length ? "Recent" : ""} items={recent} activeId={activeId} onSelect={onSelect} {...actionProps()} />
      </div>
      <div className="border-t p-2">
        <Button variant="ghost" size="sm" className="w-full justify-start text-xs text-muted-foreground" onClick={() => setShowArchived((v) => !v)}>
          <Archive className="size-3.5" />
          {showArchived ? "Hide archived" : "Show archived"}
        </Button>
      </div>

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete "{deleteTarget?.title}"?</AlertDialogTitle>
            <AlertDialogDescription>This deletes the conversation from this browser. It can't be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deleteConversation(deleteTarget.id)
                  if (activeId === deleteTarget.id) onNew()
                }
                setDeleteTarget(null)
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )

  function actionProps() {
    return {
      renamingId,
      renameValue,
      setRenameValue,
      onStartRename: (c: AiConversation) => {
        setRenamingId(c.id)
        setRenameValue(c.title)
      },
      onCommitRename: () => {
        if (renamingId) renameConversation(renamingId, renameValue.trim() || "Untitled")
        setRenamingId(null)
      },
      onCancelRename: () => setRenamingId(null),
      onDuplicate: (c: AiConversation) => {
        const copy = duplicateConversation(c.id)
        if (copy) onSelect(copy.id)
      },
      onPin: (c: AiConversation) => togglePin(c.id),
      onFavorite: (c: AiConversation) => toggleFavorite(c.id),
      onArchive: (c: AiConversation) => {
        toggleArchive(c.id)
        toast.success(c.archived ? "Unarchived" : "Archived")
      },
      onDelete: setDeleteTarget,
      onExportMd: (c: AiConversation) => download(`${c.title}.md`, exportMarkdown(c.id), "text/markdown"),
      onExportJson: (c: AiConversation) => download(`${c.title}.json`, exportJSON(c.id), "application/json"),
    }
  }
}

interface ActionProps {
  renamingId: string | null
  renameValue: string
  setRenameValue: (v: string) => void
  onStartRename: (c: AiConversation) => void
  onCommitRename: () => void
  onCancelRename: () => void
  onDuplicate: (c: AiConversation) => void
  onPin: (c: AiConversation) => void
  onFavorite: (c: AiConversation) => void
  onArchive: (c: AiConversation) => void
  onDelete: (c: AiConversation) => void
  onExportMd: (c: AiConversation) => void
  onExportJson: (c: AiConversation) => void
}

function Section({
  title,
  items,
  activeId,
  onSelect,
  ...actions
}: { title: string; items: AiConversation[]; activeId: string | null; onSelect: (id: string) => void } & ActionProps) {
  if (items.length === 0) return null
  return (
    <div className="mb-2">
      {title && <p className="px-2 py-1 text-[11px] font-medium uppercase text-muted-foreground">{title}</p>}
      {items.map((c) => (
        <ConversationRow key={c.id} conv={c} active={c.id === activeId} onSelect={onSelect} {...actions} />
      ))}
    </div>
  )
}

function ConversationRow({
  conv,
  active,
  onSelect,
  renamingId,
  renameValue,
  setRenameValue,
  onStartRename,
  onCommitRename,
  onCancelRename,
  onDuplicate,
  onPin,
  onFavorite,
  onArchive,
  onDelete,
  onExportMd,
  onExportJson,
}: { conv: AiConversation; active: boolean; onSelect: (id: string) => void } & ActionProps) {
  const isRenaming = renamingId === conv.id

  return (
    <div
      className={cn(
        "group flex items-center gap-1 rounded-md px-2 py-1.5 text-sm hover:bg-accent",
        active && "bg-accent",
      )}
    >
      {isRenaming ? (
        <Input
          autoFocus
          value={renameValue}
          onChange={(e) => setRenameValue(e.target.value)}
          onBlur={onCommitRename}
          onKeyDown={(e) => {
            if (e.key === "Enter") onCommitRename()
            if (e.key === "Escape") onCancelRename()
          }}
          className="h-6 flex-1 px-1 text-sm"
        />
      ) : (
        <button type="button" onClick={() => onSelect(conv.id)} className="min-w-0 flex-1 truncate text-left">
          {conv.pinned && <Pin className="mr-1 inline size-3 text-muted-foreground" />}
          {conv.favorite && <Star className="mr-1 inline size-3 fill-amber-400 text-amber-400" />}
          {conv.title}
          <span className="ml-1.5 text-[11px] text-muted-foreground">{formatRelativeTime(new Date(conv.updatedAt).toISOString())}</span>
        </button>
      )}
      {!isRenaming && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6 shrink-0 opacity-0 group-hover:opacity-100" aria-label="Conversation actions">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => onStartRename(conv)}>
              <Pencil /> Rename
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onDuplicate(conv)}>
              <Copy /> Duplicate
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onPin(conv)}>
              <Pin /> {conv.pinned ? "Unpin" : "Pin"}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onFavorite(conv)}>
              <Star /> {conv.favorite ? "Unfavorite" : "Favorite"}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => onExportMd(conv)}>
              <Download /> Export Markdown
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onExportJson(conv)}>
              <Download /> Export JSON
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => onArchive(conv)}>
              {conv.archived ? <ArchiveRestore /> : <Archive />} {conv.archived ? "Unarchive" : "Archive"}
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" onClick={() => onDelete(conv)}>
              <Trash2 /> Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}
