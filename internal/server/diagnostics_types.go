// Package server: diagnostics_types.go defines the shared vocabulary the
// whole provider-diagnostics subsystem (diagnostics_ollama.go,
// diagnostics_openai_compat.go, diagnostics_errors.go,
// diagnostics_handlers.go) speaks — one report shape regardless of which
// provider produced it, so the frontend renders one set of panels instead
// of a per-provider special case. See providerDiagnoser's doc comment for
// the extensibility contract this exists to support.
package server

import "time"

// capabilitySource records HOW a capability claim was determined —
// central to this subsystem's "never fake data" requirement. A capability
// is either read directly off a live response from the provider itself
// (capabilitySourceLive — e.g. Ollama's own /api/tags "capabilities"
// array, or "the model actually replied to a tool-call-eliciting probe"),
// or it's looked up in a small maintained table by model family and only
// ever applied to a model ID the SAME diagnostic run already confirmed
// exists via a live call (capabilitySourceKnown) — hosted providers
// (Anthropic, OpenAI, ...) simply don't expose a capabilities endpoint, so
// there is no live signal to read for "does this model support vision."
// The frontend renders these two sources with different affordances (a
// live checkmark vs. a "known" badge with a tooltip explaining why) rather
// than presenting a guess as a fact.
type capabilitySource string

const (
	capabilitySourceLive    capabilitySource = "live"
	capabilitySourceKnown   capabilitySource = "known"
	capabilitySourceUnknown capabilitySource = "unknown"
)

// capability is one yes/no/unknown fact about a provider or model —
// Section 6's "AI Capabilities Panel".
type capability struct {
	Name      string           `json:"name"`
	Supported bool             `json:"supported"`
	Source    capabilitySource `json:"source"`
	// Detail explains the Source in one line — "reported by /api/tags",
	// "known for the claude-sonnet family, verified via live /v1/models
	// call", "not exposed by this provider's API" — so the UI never shows
	// a bare checkmark with no way to ask "how do we know that."
	Detail string `json:"detail"`
}

// modelInfo is one entry in Section 2's Model Discovery panel — a real
// installed/available model, never a guessed or hardcoded one. Fields
// that a provider genuinely doesn't report (e.g. Anthropic never reports
// on-disk size or quantization — those are Ollama-only, local-model
// concepts) are left at their zero value and the frontend omits them
// rather than rendering "0 B".
type modelInfo struct {
	Name string `json:"name"`
	// SizeBytes/Quantization/ParameterSize are Ollama-only — the on-disk
	// size and quant level of a locally-pulled model, read straight from
	// /api/tags's own "size"/"details" fields (real data, not computed).
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	ParameterSize string `json:"parameter_size,omitempty"`
	Family        string `json:"family,omitempty"`
	// ContextLength is populated when the provider actually reports it —
	// Ollama's /api/tags does (per-model, in recent daemon versions); for
	// Anthropic/OpenAI it comes from the same known-model-family table
	// capabilities does, and Source below says which.
	ContextLength int              `json:"context_length,omitempty"`
	IsEmbedding   bool             `json:"is_embedding"`
	IsVision      bool             `json:"is_vision"`
	IsDefault     bool             `json:"is_default"`
	IsSelected    bool             `json:"is_selected"`
	Source        capabilitySource `json:"source"`
}

// diagnosticSeverity is the top-level traffic light for one issue or one
// whole report.
type diagnosticSeverity string

const (
	severityOK    diagnosticSeverity = "ok"
	severityWarn  diagnosticSeverity = "warning"
	severityError diagnosticSeverity = "error"
)

// fixAction is a machine-readable identifier the frontend switches on to
// decide what a "one-click fix" button actually does — Section 9. Every
// action here has a real handler; there is deliberately no
// "restart_ollama" or "start_ollama" action (see diagnosticFix's doc
// comment for why the backend never spawns local processes on the
// operator's machine).
type fixAction string

const (
	fixActionPullModel     fixAction = "pull_model"
	fixActionRefreshModels fixAction = "refresh_models"
	fixActionRetest        fixAction = "retest"
	fixActionCopyCurl      fixAction = "copy_curl"
	fixActionOpenLogs      fixAction = "open_logs"
	fixActionOpenDocs      fixAction = "open_docs"
	fixActionOpenSettings  fixAction = "open_settings"
	fixActionCheckBaseURL  fixAction = "check_base_url"
)

// diagnosticFix is one suggested, concrete action attached to an issue —
// Section 9. Command carries whatever the action needs to actually run
// (a model name for pull_model, a ready-to-copy curl string for
// copy_curl, a URL for open_docs) so the frontend never has to
// reconstruct it. Deliberately excludes anything that would mean the
// backend executing an arbitrary local process on the operator's host
// (e.g. "start Ollama for me") — a web server spawning processes on
// whatever machine it happens to be running on is a real security
// liability (this binary is meant to be deployable, not just run on a
// laptop next to Ollama) and Ollama itself offers no remote "start"
// endpoint to call instead. copy_curl gives the operator the exact
// command to run themselves, which is both safer and more transparent
// than a hidden exec() call would be.
type diagnosticFix struct {
	Label   string    `json:"label"`
	Action  fixAction `json:"action"`
	Command string    `json:"command,omitempty"`
}

// diagnosticIssue is one concrete, explained problem — Section 4's "Live
// Diagnostics": never a bare "couldn't reach Ollama", always a code, a
// human summary, why it might be happening, and what to do about it.
type diagnosticIssue struct {
	Code           string             `json:"code"`
	Severity       diagnosticSeverity `json:"severity"`
	Summary        string             `json:"summary"`
	PossibleCauses []string           `json:"possible_causes,omitempty"`
	Fixes          []diagnosticFix    `json:"fixes,omitempty"`
}

// checkResult is one individual probe inside a provider report — "GET
// /api/tags", "streaming chat call", "tool-calling probe" — each with its
// own pass/fail and latency, so the UI can show exactly which of several
// checks failed instead of one opaque overall status. Section 3's
// per-provider checklists map directly onto a list of these.
type checkResult struct {
	Name      string             `json:"name"`
	Status    diagnosticSeverity `json:"status"`
	Detail    string             `json:"detail"`
	LatencyMS int64              `json:"latency_ms,omitempty"`
}

// providerDiagnosticsReport is the one shape every provider's diagnostics
// run produces — Sections 1, 2, 3, 5, and 6 all read off this single
// struct. Zero-value/omitted fields mean "this provider doesn't report
// that," never "we didn't check."
type providerDiagnosticsReport struct {
	Provider  string             `json:"provider"`
	Label     string             `json:"label"` // human display name, e.g. "Ollama", "Anthropic"
	Status    diagnosticSeverity `json:"status"`
	Reachable bool               `json:"reachable"`
	BaseURL   string             `json:"base_url,omitempty"`
	// Version is the provider's own reported version — Ollama's daemon
	// version from /api/version; empty for hosted APIs that don't expose
	// one (Anthropic/OpenAI have no public "server version" concept).
	Version      string `json:"version,omitempty"`
	LatencyMS    int64  `json:"latency_ms"`
	CheckedAt    string `json:"checked_at"`
	TotalCheckMS int64  `json:"total_check_ms"`

	Checks []checkResult `json:"checks"`

	Models        []modelInfo `json:"models,omitempty"`
	DefaultModel  string      `json:"default_model,omitempty"`
	SelectedModel string      `json:"selected_model,omitempty"`
	// SelectedModelFound is only meaningful when SelectedModel is
	// non-empty — false means the currently-configured chat model isn't
	// actually available from this provider right now (Section 2's
	// "missing model" warning).
	SelectedModelFound bool `json:"selected_model_found"`

	EmbeddingModel      string `json:"embedding_model,omitempty"`
	EmbeddingModelFound bool   `json:"embedding_model_found"`

	Capabilities []capability `json:"capabilities"`

	// ContextWindow/MaxTokens describe the SELECTED model specifically
	// (not the provider in the abstract) — omitted when unknown rather
	// than showing a wrong or made-up number.
	ContextWindow int `json:"context_window,omitempty"`
	MaxTokens     int `json:"max_tokens,omitempty"`

	// UptimeSeconds is Ollama-only (best-effort, from /api/ps's oldest
	// loaded-model expiry as a lower bound is NOT reliable enough to
	// report — see diagnostics_ollama.go's doc comment — so this is
	// actually left unset for every provider today; the field stays
	// here, honestly always omitted, rather than removed, because a
	// future Ollama version or a self-hosted OpenAI-compatible gateway
	// that DOES expose real uptime should be able to populate it without
	// a wire-format change).
	UptimeSeconds *float64 `json:"uptime_seconds,omitempty"`

	Issues []diagnosticIssue `json:"issues,omitempty"`
}

// normalizeSlices guarantees Checks/Capabilities/Issues are never a nil
// Go slice by the time this report is serialized. A nil slice and an
// empty slice carry the exact same meaning for these three fields
// (there is no "never checked" state distinct from "checked, found
// none" — unlike, say, capabilitySource's live/known/unknown), but
// encoding/json marshals a nil slice as JSON null and an empty slice as
// [], so without this every diagnose* code path that returns before
// appending anything (e.g. no API key configured) would silently hand
// the frontend a null where it always expects an array. Called once,
// centrally, in handleProviderDiagnostics — new diagnose* functions get
// the guarantee for free instead of every call site having to remember
// to initialize its own report literal correctly.
func (r *providerDiagnosticsReport) normalizeSlices() {
	if r.Checks == nil {
		r.Checks = []checkResult{}
	}
	if r.Capabilities == nil {
		r.Capabilities = []capability{}
	}
	if r.Issues == nil {
		r.Issues = []diagnosticIssue{}
	}
}

// diagnosticsHistoryEntry is one persisted row — Section 7's "Connection
// History". Kept deliberately small (no full report body) — see
// diagnostics_handlers.go's migration for why.
type diagnosticsHistoryEntry struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	Success    bool   `json:"success"`
	Summary    string `json:"summary"`
	DurationMS int64  `json:"duration_ms"`
	CreatedAt  string `json:"created_at"`
}

// performanceSummary is Section 5's Performance Panel, derived from
// diagnosticsHistoryEntry rows for one provider — never a single point-in-
// time number presented as if it were an average.
type performanceSummary struct {
	Provider           string   `json:"provider"`
	AvgResponseMS      *float64 `json:"avg_response_ms,omitempty"`
	LastLatencyMS      *int64   `json:"last_latency_ms,omitempty"`
	LastSuccessAt      string   `json:"last_success_at,omitempty"`
	LastFailureAt      string   `json:"last_failure_at,omitempty"`
	SuccessRatePercent *float64 `json:"success_rate_percent,omitempty"`
	// HealthScore is 0-100, computed from SuccessRatePercent and recent
	// latency relative to this provider's own history — never an
	// arbitrary made-up number; see computeHealthScore's doc comment.
	HealthScore int      `json:"health_score"`
	Warnings    []string `json:"warnings,omitempty"`
	SampleCount int      `json:"sample_count"`
}

func now() time.Time { return time.Now() }
