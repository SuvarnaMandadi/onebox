import { AlertTriangle, Check, Database, HardDrive, Package, RefreshCw, X } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { formatBytes } from "@/lib/file-render"
import { formatRelativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { BackendHealthReport } from "@/lib/diagnostics-types"

function StatusIcon({ ok, error }: { ok: boolean; error?: string }) {
  if (ok) return <Check className="size-4 text-emerald-600 dark:text-emerald-400" />
  return error ? <X className="size-4 text-destructive" /> : <AlertTriangle className="size-4 text-amber-600 dark:text-amber-400" />
}

function StatCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-lg border p-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="mt-1 text-xl font-semibold">{value}</p>
      {sub && <p className="text-xs text-muted-foreground">{sub}</p>}
    </div>
  )
}

export function BackendHealthPanel({
  report,
  loading,
  onRefresh,
}: {
  report: BackendHealthReport | null
  loading: boolean
  onRefresh: () => void
}) {
  if (!report) {
    return (
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {Array.from({ length: 8 }).map((_, i) => (
          <Skeleton key={i} className="h-20" />
        ))}
      </div>
    )
  }

  const { database, file_storage, disk, process, backup_scheduler, counts, build } = report

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted-foreground">Checked {formatRelativeTime(report.checked_at)}</p>
        <Button size="sm" variant="ghost" onClick={onRefresh} disabled={loading}>
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} /> Refresh
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Collections" value={counts.collections.toLocaleString()} />
        <StatCard label="Records" value={counts.records.toLocaleString()} />
        <StatCard label="Files" value={counts.files.toLocaleString()} sub={formatBytes(file_storage.total_size_bytes)} />
        <StatCard label="Backups" value={counts.backups.toLocaleString()} sub={backup_scheduler.last_backup_at ? `last ${formatRelativeTime(backup_scheduler.last_backup_at)}` : "none yet"} />
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="rounded-lg border p-3">
          <div className="mb-2 flex items-center gap-2 text-sm font-medium">
            <Database className="size-4" /> Database
          </div>
          <div className="space-y-1.5 text-sm">
            <div className="flex items-center gap-2">
              <StatusIcon ok={database.reachable} error={database.error} />
              <span>{database.reachable ? "Reachable" : "Unreachable"}</span>
              <span className="ml-auto text-xs text-muted-foreground">{database.latency_ms}ms</span>
            </div>
            <div className="flex items-center gap-2">
              <StatusIcon ok={database.wal_mode} />
              <span>WAL mode {database.wal_mode ? "on" : "off"}</span>
            </div>
            <div className="flex items-center gap-2">
              <StatusIcon ok={database.integrity_ok} />
              <span>Integrity check {database.integrity_ok ? "passed" : "not verified"}</span>
            </div>
            {!!database.size_bytes && <p className="text-xs text-muted-foreground">{formatBytes(database.size_bytes)} on disk</p>}
            {database.error && <p className="text-xs text-destructive">{database.error}</p>}
          </div>
        </div>

        <div className="rounded-lg border p-3">
          <div className="mb-2 flex items-center gap-2 text-sm font-medium">
            <HardDrive className="size-4" /> Disk
          </div>
          {disk.available ? (
            <div className="space-y-1.5">
              <Progress value={disk.used_percent ?? 0} />
              <p className="text-xs text-muted-foreground">
                {formatBytes((disk.total_bytes ?? 0) - (disk.free_bytes ?? 0))} used of {formatBytes(disk.total_bytes ?? 0)} (
                {(disk.used_percent ?? 0).toFixed(1)}%)
              </p>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">Disk usage isn't available on this platform{disk.error ? `: ${disk.error}` : "."}</p>
          )}
        </div>

        <div className="rounded-lg border p-3">
          <div className="mb-2 flex items-center gap-2 text-sm font-medium">
            <Package className="size-4" /> Backup scheduler
          </div>
          <div className="space-y-1 text-sm">
            <div className="flex items-center gap-2">
              <Badge variant={backup_scheduler.enabled ? "secondary" : "outline"} className="text-[10px]">
                {backup_scheduler.enabled ? "scheduled" : "manual only"}
              </Badge>
              {backup_scheduler.enabled && (
                <span className="text-xs text-muted-foreground">
                  every {backup_scheduler.interval_hours}h, keep {backup_scheduler.retention_count}
                </span>
              )}
            </div>
            <p className="text-xs text-muted-foreground">
              {backup_scheduler.backup_count} backup{backup_scheduler.backup_count === 1 ? "" : "s"} on file
              {backup_scheduler.last_backup_status && ` — last ${backup_scheduler.last_backup_status}`}
            </p>
          </div>
        </div>

        <div className="rounded-lg border p-3">
          <div className="mb-2 text-sm font-medium">Process</div>
          <div className="space-y-1 text-xs text-muted-foreground">
            <p>Uptime: {formatUptime(process.uptime_seconds)}</p>
            <p>Heap: {formatBytes(process.heap_alloc_bytes)} / {formatBytes(process.heap_sys_bytes)}</p>
            <p>Goroutines: {process.goroutines}</p>
          </div>
        </div>
      </div>

      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>Version {build.version}</span>
        <span>Commit {build.commit}</span>
        <span>{build.migration_count} migrations applied (latest: {build.latest_migration || "none"})</span>
      </div>
    </div>
  )
}

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`
  const mins = Math.floor(seconds / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ${mins % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}
