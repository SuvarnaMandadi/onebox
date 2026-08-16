import { Check, X } from "lucide-react"
import { formatRelativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DiagnosticsHistoryEntry, PerformanceSummary } from "@/lib/diagnostics-types"

function healthScoreTone(score: number): string {
  if (score >= 90) return "text-emerald-600 dark:text-emerald-400"
  if (score >= 60) return "text-amber-600 dark:text-amber-400"
  return "text-destructive"
}

export function PerformancePanel({ summary }: { summary: PerformanceSummary | null }) {
  if (!summary || summary.sample_count === 0) {
    return <p className="text-sm text-muted-foreground">No checks recorded yet for this provider.</p>
  }
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <div className={cn("text-3xl font-bold", healthScoreTone(summary.health_score))}>{summary.health_score}</div>
        <div className="text-xs text-muted-foreground">
          Health score
          <br />
          from last {summary.sample_count} check{summary.sample_count === 1 ? "" : "s"}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-4">
        <div>
          <p className="text-muted-foreground">Avg response</p>
          <p className="font-medium">{summary.avg_response_ms != null ? `${Math.round(summary.avg_response_ms)}ms` : "—"}</p>
        </div>
        <div>
          <p className="text-muted-foreground">Last latency</p>
          <p className="font-medium">{summary.last_latency_ms != null ? `${summary.last_latency_ms}ms` : "—"}</p>
        </div>
        <div>
          <p className="text-muted-foreground">Success rate</p>
          <p className="font-medium">{summary.success_rate_percent != null ? `${summary.success_rate_percent.toFixed(0)}%` : "—"}</p>
        </div>
        <div>
          <p className="text-muted-foreground">Last success</p>
          <p className="font-medium">{summary.last_success_at ? formatRelativeTime(summary.last_success_at) : "never"}</p>
        </div>
      </div>
      {!!summary.warnings?.length && (
        <ul className="list-disc space-y-0.5 pl-4 text-xs text-amber-600 dark:text-amber-400">
          {summary.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

export function ConnectionHistoryPanel({ entries }: { entries: DiagnosticsHistoryEntry[] | null }) {
  const items = entries ?? []
  if (items.length === 0) {
    return <p className="text-sm text-muted-foreground">No connection tests recorded yet — run diagnostics to start building history.</p>
  }
  return (
    <div className="max-h-64 space-y-1 overflow-y-auto">
      {items.map((e) => (
        <div key={e.id} className="flex items-center gap-2 border-b py-1.5 text-xs last:border-0">
          {e.success ? (
            <Check className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <X className="size-3.5 shrink-0 text-destructive" />
          )}
          <span className="w-16 shrink-0 capitalize text-muted-foreground">{e.provider}</span>
          <span className="truncate">{e.summary}</span>
          <span className="ml-auto shrink-0 text-muted-foreground">{e.duration_ms}ms</span>
          <span className="w-20 shrink-0 text-right text-muted-foreground">{formatRelativeTime(e.created_at)}</span>
        </div>
      ))}
    </div>
  )
}
