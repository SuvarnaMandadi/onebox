import { ChevronDown, Check, Database, FileSearch, Info, Link2, ListTree, Plus } from "lucide-react"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import type { ExecutedAction } from "@/lib/ai-types"

// The AI Workspace's Activity Panel — Milestone 5's "signature feature."
// Renders exactly what chatbotResponse.executed_actions/streamDoneEvent
// carries: Type/Title/Description, all plain human strings the backend
// already generates for its own Proposal Cards (see executedActionSummary's
// doc comment) — never a tool-call payload, never provider JSON.
const ICONS: Record<string, typeof Database> = {
  create_collection: Plus,
  add_field: Plus,
  describe_onebox: Info,
  list_collections: ListTree,
  list_records: FileSearch,
  // find_related_records is the Milestone 6 relation-traversal tool (see
  // executeFindRelatedRecords, chatbot_tool_execution.go) — its
  // Description ("Find records related to orders/abc123") is already the
  // same human-readable, no-raw-JSON text every other row here renders;
  // only a distinct icon was needed.
  find_related_records: Link2,
}

export function ToolActivity({ actions }: { actions: ExecutedAction[] }) {
  if (actions.length === 0) return null
  return (
    <Collapsible defaultOpen={actions.length <= 2} className="w-full max-w-md">
      <CollapsibleTrigger className="group flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground">
        <ChevronDown className="size-3 transition-transform group-data-[state=open]:rotate-180" />
        {actions.length} step{actions.length === 1 ? "" : "s"} taken
      </CollapsibleTrigger>
      <CollapsibleContent className="mt-1.5 space-y-1 rounded-md border bg-muted/30 p-2">
        {actions.map((a, i) => {
          const Icon = ICONS[a.type] ?? Database
          return (
            <div key={i} className="flex items-start gap-2 text-xs">
              <Check className="mt-0.5 size-3 shrink-0 text-green-600 dark:text-green-500" />
              <Icon className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
              <span className="text-muted-foreground">{a.description || a.title}</span>
            </div>
          )
        })}
      </CollapsibleContent>
    </Collapsible>
  )
}
