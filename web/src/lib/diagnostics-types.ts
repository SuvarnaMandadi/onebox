// Mirrors the JSON shapes internal/server/diagnostics_types.go,
// backend_health.go, and diagnostics_handlers.go actually serialize —
// every field here corresponds one-to-one with a real Go struct field,
// not a guess at what the backend "probably" returns.

export type Severity = "ok" | "warning" | "error"
export type CapabilitySource = "live" | "known" | "unknown"

export interface Capability {
  name: string
  supported: boolean
  source: CapabilitySource
  detail: string
}

export interface ModelInfo {
  name: string
  size_bytes?: number
  quantization?: string
  parameter_size?: string
  family?: string
  context_length?: number
  is_embedding: boolean
  is_vision: boolean
  is_default: boolean
  is_selected: boolean
  source: CapabilitySource
}

export type FixAction =
  | "pull_model"
  | "refresh_models"
  | "retest"
  | "copy_curl"
  | "open_logs"
  | "open_docs"
  | "open_settings"
  | "check_base_url"

export interface DiagnosticFix {
  label: string
  action: FixAction
  command?: string
}

export interface DiagnosticIssue {
  code: string
  severity: Severity
  summary: string
  possible_causes?: string[]
  fixes?: DiagnosticFix[]
}

export interface CheckResult {
  name: string
  status: Severity
  detail: string
  latency_ms?: number
}

export interface ProviderDiagnosticsReport {
  provider: string
  label: string
  status: Severity
  reachable: boolean
  base_url?: string
  version?: string
  latency_ms: number
  checked_at: string
  total_check_ms: number
  checks: CheckResult[]
  models?: ModelInfo[]
  default_model?: string
  selected_model?: string
  selected_model_found: boolean
  embedding_model?: string
  embedding_model_found: boolean
  capabilities: Capability[]
  context_window?: number
  max_tokens?: number
  uptime_seconds?: number
  issues?: DiagnosticIssue[]
}

export interface DiagnosticsHistoryEntry {
  id: string
  provider: string
  success: boolean
  summary: string
  duration_ms: number
  created_at: string
}

export interface PerformanceSummary {
  provider: string
  avg_response_ms?: number
  last_latency_ms?: number
  last_success_at?: string
  last_failure_at?: string
  success_rate_percent?: number
  health_score: number
  warnings?: string[]
  sample_count: number
}

export interface ConfigIssue {
  field: string
  message: string
}

export interface OllamaPullProgress {
  status: string
  digest?: string
  total?: number
  completed?: number
  error?: string
  done?: boolean
}

// -- Backend Health (Section 10) --------------------------------------------

export interface DbHealth {
  reachable: boolean
  wal_mode: boolean
  integrity_ok: boolean
  size_bytes: number
  error?: string
  latency_ms: number
}

export interface FileStorageHealth {
  reachable: boolean
  file_count: number
  total_size_bytes: number
  error?: string
}

export interface DiskHealth {
  available: boolean
  free_bytes?: number
  total_bytes?: number
  used_percent?: number
  error?: string
}

export interface ProcessMetrics {
  goroutines: number
  heap_alloc_bytes: number
  heap_sys_bytes: number
  gc_cpu_fraction: number
  uptime_seconds: number
}

export interface BackupSchedulerHealth {
  enabled: boolean
  interval_hours?: number
  retention_count?: number
  backup_count: number
  last_backup_at?: string
  last_backup_status?: string
}

export interface EntityCounts {
  collections: number
  records: number
  files: number
  backups: number
}

export interface BuildInfo {
  version: string
  commit: string
  migration_count: number
  latest_migration: string
}

export interface BackendHealthReport {
  database: DbHealth
  file_storage: FileStorageHealth
  disk: DiskHealth
  process: ProcessMetrics
  backup_scheduler: BackupSchedulerHealth
  counts: EntityCounts
  build: BuildInfo
  checked_at: string
}
