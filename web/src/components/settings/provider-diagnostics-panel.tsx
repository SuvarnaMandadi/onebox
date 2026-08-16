import { useState } from "react"
import {
  AlertTriangle,
  Check,
  ChevronDown,
  Clock,
  Copy,
  Download,
  ExternalLink,
  RefreshCw,
  X,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { toast } from "sonner"
import { formatBytes } from "@/lib/file-render"
import { cn } from "@/lib/utils"
import type { Capability, DiagnosticFix, ModelInfo, ProviderDiagnosticsReport, Severity } from "@/lib/diagnostics-types"

// severityTone centralizes the ok/warning/error -> color mapping so every
// panel in this subsystem (this one, connection history, backend health)
// renders the exact same traffic-light semantics rather than each
// component inventing its own.
export function severityTone(s: Severity): string {
  switch (s) {
    case "ok":
      return "text-emerald-600 dark:text-emerald-400"
    case "warning":
      return "text-amber-600 dark:text-amber-400"
    default:
      return "text-destructive"
  }
}

function SeverityBadge({ status }: { status: Severity }) {
  const label = status === "ok" ? "Healthy" : status === "warning" ? "Warning" : "Error"
  const variant = status === "ok" ? "secondary" : status === "error" ? "destructive" : "outline"
  return (
    <Badge variant={variant} className={cn(status === "ok" && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400")}>
      {label}
    </Badge>
  )
}

function SourceBadge({ source }: { source: Capability["source"] | ModelInfo["source"] }) {
  if (source === "live") {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge variant="outline" className="gap-1 text-[10px] font-normal">
            <Check className="size-3" /> live
          </Badge>
        </TooltipTrigger>
        <TooltipContent>Verified against the provider's own response this check.</TooltipContent>
      </Tooltip>
    )
  }
  if (source === "known") {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge variant="outline" className="text-[10px] font-normal text-muted-foreground">
            known
          </Badge>
        </TooltipTrigger>
        <TooltipContent>This provider has no capabilities API — based on the known model family, not queried live.</TooltipContent>
      </Tooltip>
    )
  }
  return null
}

export function FixButton({
  fix,
  onPullModel,
  onRefresh,
  onRetest,
}: {
  fix: DiagnosticFix
  onPullModel?: (model: string) => void
  onRefresh?: () => void
  onRetest?: () => void
}) {
  switch (fix.action) {
    case "pull_model":
      return (
        <Button size="sm" variant="outline" onClick={() => onPullModel?.(fix.command ?? "")}>
          <Download /> {fix.label}
        </Button>
      )
    case "refresh_models":
      return (
        <Button size="sm" variant="outline" onClick={onRefresh}>
          <RefreshCw /> {fix.label}
        </Button>
      )
    case "retest":
      return (
        <Button size="sm" variant="outline" onClick={onRetest}>
          <RefreshCw /> {fix.label}
        </Button>
      )
    case "copy_curl":
      return (
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            navigator.clipboard.writeText(fix.command ?? "")
            toast.success("curl command copied")
          }}
        >
          <Copy /> {fix.label}
        </Button>
      )
    case "open_docs":
      return (
        <Button size="sm" variant="outline" asChild>
          <a href={fix.command} target="_blank" rel="noreferrer">
            <ExternalLink /> {fix.label}
          </a>
        </Button>
      )
    case "open_logs":
      return (
        <Button size="sm" variant="outline" asChild>
          <a href="/app/settings#backend-health">
            <ExternalLink /> {fix.label}
          </a>
        </Button>
      )
    default:
      // check_base_url / open_settings: scroll the operator back to the
      // config form rather than a dead button.
      return (
        <Button size="sm" variant="outline" onClick={() => document.getElementById("provider-config-form")?.scrollIntoView({ behavior: "smooth" })}>
          {fix.label}
        </Button>
      )
  }
}

function ModelRow({ model }: { model: ModelInfo }) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm">
      <span className="font-mono text-xs font-medium">{model.name}</span>
      {model.is_selected && <Badge className="text-[10px]">selected</Badge>}
      {model.is_default && <Badge variant="outline" className="text-[10px]">default</Badge>}
      {model.is_embedding && <Badge variant="outline" className="text-[10px]">embedding</Badge>}
      {model.is_vision && <Badge variant="outline" className="text-[10px]">vision</Badge>}
      <span className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
        {model.parameter_size && <span>{model.parameter_size}</span>}
        {model.quantization && <span>{model.quantization}</span>}
        {!!model.size_bytes && <span>{formatBytes(model.size_bytes)}</span>}
        {!!model.context_length && <span>{model.context_length.toLocaleString()} ctx</span>}
      </span>
    </div>
  )
}

export function ProviderDiagnosticsPanel({
  report,
  loading,
  onPullModel,
  onRefresh,
  onRetest,
}: {
  report: ProviderDiagnosticsReport | null
  loading: boolean
  onPullModel: (model: string) => void
  onRefresh: () => void
  onRetest: () => void
}) {
  const [modelsOpen, setModelsOpen] = useState(false)

  if (loading && !report) {
    return <p className="text-sm text-muted-foreground">Running diagnostics…</p>
  }
  if (!report) {
    return <p className="text-sm text-muted-foreground">Run diagnostics to see live status.</p>
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <SeverityBadge status={report.status} />
        <span className="text-sm font-medium">{report.label}</span>
        {report.base_url && <span className="text-xs text-muted-foreground">{report.base_url}</span>}
        {report.version && <Badge variant="outline" className="text-[10px]">v{report.version}</Badge>}
        <span className="ml-auto flex items-center gap-1 text-xs text-muted-foreground">
          <Clock className="size-3" /> {report.latency_ms}ms
        </span>
        <Button size="sm" variant="ghost" onClick={onRetest} disabled={loading}>
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} /> Re-test
        </Button>
      </div>

      {/* Section 3/4: individual checks */}
      {report.checks.length > 0 && (
        <div className="space-y-1.5">
          {report.checks.map((c) => (
            <div key={c.name} className="flex items-center gap-2 text-sm">
              {c.status === "ok" ? (
                <Check className={cn("size-4 shrink-0", severityTone("ok"))} />
              ) : c.status === "warning" ? (
                <AlertTriangle className={cn("size-4 shrink-0", severityTone("warning"))} />
              ) : (
                <X className={cn("size-4 shrink-0", severityTone("error"))} />
              )}
              <span>{c.name}</span>
              <span className="text-xs text-muted-foreground">{c.detail}</span>
              {!!c.latency_ms && <span className="ml-auto text-xs text-muted-foreground">{c.latency_ms}ms</span>}
            </div>
          ))}
        </div>
      )}

      {/* Section 4: issues, each with causes + fixes */}
      {!!report.issues?.length && (
        <div className="space-y-3">
          {report.issues.map((issue, i) => (
            <div key={i} className={cn("rounded-md border p-3", issue.severity === "error" ? "border-destructive/40 bg-destructive/5" : "border-amber-500/40 bg-amber-500/5")}>
              <p className="text-sm font-medium">{issue.summary}</p>
              {!!issue.possible_causes?.length && (
                <ul className="mt-1.5 list-disc space-y-0.5 pl-4 text-xs text-muted-foreground">
                  {issue.possible_causes.map((c, j) => (
                    <li key={j}>{c}</li>
                  ))}
                </ul>
              )}
              {!!issue.fixes?.length && (
                <div className="mt-2 flex flex-wrap gap-2">
                  {issue.fixes.map((f, j) => (
                    <FixButton key={j} fix={f} onPullModel={onPullModel} onRefresh={onRefresh} onRetest={onRetest} />
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Section 6: capabilities */}
      {!!report.capabilities.length && (
        <div>
          <p className="mb-1.5 text-xs font-medium text-muted-foreground">Capabilities</p>
          <div className="flex flex-wrap gap-1.5">
            {report.capabilities.map((c) => (
              <div key={c.name} className="flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs">
                {c.supported ? <Check className="size-3 text-emerald-600 dark:text-emerald-400" /> : <X className="size-3 text-muted-foreground" />}
                <span className={!c.supported ? "text-muted-foreground" : ""}>{c.name}</span>
                <SourceBadge source={c.source} />
              </div>
            ))}
          </div>
        </div>
      )}

      {(!!report.context_window || !!report.max_tokens) && (
        <div className="flex gap-4 text-xs text-muted-foreground">
          {!!report.context_window && <span>Context window: {report.context_window.toLocaleString()} tokens</span>}
          {!!report.max_tokens && <span>Max output: {report.max_tokens.toLocaleString()} tokens</span>}
        </div>
      )}

      {/* Section 2: model discovery */}
      {!!report.models?.length && (
        <Collapsible open={modelsOpen} onOpenChange={setModelsOpen}>
          <CollapsibleTrigger asChild>
            <Button variant="ghost" size="sm" className="h-7 px-2 text-xs">
              <ChevronDown className={cn("size-3.5 transition-transform", modelsOpen && "rotate-180")} />
              {report.models.length} model{report.models.length === 1 ? "" : "s"} discovered
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="mt-2 space-y-1.5">
            {report.models.map((m) => (
              <ModelRow key={m.name} model={m} />
            ))}
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}
