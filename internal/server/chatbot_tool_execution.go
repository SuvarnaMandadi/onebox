package server

import (
	"context"
	"fmt"
	"strings"

	"onebox/internal/llm"
)

// This file is the AI Execution milestone's real-side-effect layer — the
// piece ARCHITECTURE.md's §14 "Planned milestones" previously listed as
// NOT YET BUILT (see that doc's "Approve -> Execute a validated proposal"
// entry). It sits between ProposalValidator (chatbot_proposal_validator.go
// — proves an operation is legal against real instance state) and the
// multi-round tool-execution loop (runToolLoop, chatbot_handlers.go —
// decides whether a round needs to go again): given one already-validated
// proposedAction, either perform its real side effect for real (reusing
// the exact same data-layer functions a hand-typed API request would call
// — createCollection, updateCollectionSchema, listCollections; never a
// second, drifted copy of that logic) or explain why it wasn't run.

// toolResult is one proposedAction's outcome, in the shape the model needs
// to see to write its final natural-language reply — see runToolLoop,
// which turns this into a Role:"tool" llm.Message for the next round.
type toolResult struct {
	// Action is the proposedAction this result came from — carried through
	// so the caller can still surface it as a Proposal Card when Executed
	// is false (the admin still needs to see and confirm it).
	Action proposedAction
	// Executed reports whether Action's real side effect actually ran.
	// False covers two distinct reasons — a destructive operation that
	// needs the admin's explicit confirmation, and an operation with no
	// real execution primitive built yet (see autoExecutable) — but from
	// the model's point of view both mean the identical thing: nothing
	// happened yet, so its reply must not claim it did (see
	// chatbotSystemPrompt's TRUTHFULNESS section).
	Executed bool
	// Content is the tool's plain-text result, exactly what gets sent back
	// to the model as this call's tool-result message.
	Content string
}

// autoExecutable reports whether a validated proposedAction may run
// immediately, without the admin's explicit confirmation, per the user's
// own explicit split: "For potentially destructive operations (delete,
// drop, overwrite, bulk changes), require explicit user confirmation
// before executing. For safe operations (describe OneBox, list
// collections, create simple collections, read records, etc.), execute
// automatically if the user has permission."
//
// Destructive is actionMeta's fixed, per-type flag (chatbot_actions.go) —
// checked first and always wins. Beyond that, only the five types below
// ever auto-execute, because those are the only ones with a real data-layer
// primitive to call: rename_collection has no backing function at all (see
// collections.go's doc comment — only field-level RenameFrom exists, never
// a collection-level rename), and import_data has no actual file/row data
// in this pipeline to import even though it's flagged non-destructive.
// Auto-"executing" either would mean silently doing nothing while telling
// the model it succeeded — a truthfulness violation, not a safety one — so
// both stay propose-only regardless of their Destructive flag.
func autoExecutable(a proposedAction) bool {
	if a.Destructive {
		return false
	}
	switch a.Type {
	case actionCreateCollection, actionAddField, actionDescribeOnebox, actionListCollections, actionListRecords, actionFindRelatedRecords,
		actionListBackups, actionGetRecentErrors, actionGetSettingsSummary:
		return true
	default:
		return false
	}
}

// notExecutedResult is the toolResult for a proposedAction autoExecutable
// says no to — the model is told plainly that nothing ran yet and why, so
// it never narrates a change that didn't happen.
func notExecutedResult(a proposedAction) toolResult {
	reason := "requires the admin's explicit confirmation before it runs"
	switch a.Type {
	case actionRenameCollection:
		reason = "isn't supported for automatic execution yet — it still needs to be done manually from the dashboard"
	case actionImportData:
		// Real CSV/JSON import genuinely exists (POST
		// /api/collections/:name/import(/preview)) and works today — the
		// gap is specific to this one tool call: a chat message can't
		// carry a file's actual bytes as a tool argument, so there's
		// nothing here to execute even though the feature itself isn't
		// missing. Point at where it actually lives (the classic
		// dashboard's collection page — the new /app/ dashboard has no
		// import UI yet, same gap as Settings) rather than a vague "do it
		// manually," so the model can give a real, actionable answer
		// instead of a dead end.
		reason = "can't be run from chat — there's no way to attach a file's bytes to a tool call. The import itself is real: open the collection in the classic dashboard (/_/) and use its Import button to upload a CSV/JSON file"
	}
	return toolResult{
		Action: a, Executed: false,
		Content: fmt.Sprintf("Not executed — %q %s. It has been proposed for the admin's review; do not claim it already happened.", a.Title, reason),
	}
}

// executeToolCall runs one already-validated proposedAction (validated by
// proposalValidator — this never re-validates) for real when
// autoExecutable allows it, or returns an explicit not-executed toolResult
// otherwise. ctx/s.db are threaded straight through to the same data-layer
// functions collection_handlers.go's real HTTP endpoints call.
func executeToolCall(ctx context.Context, s *Server, a proposedAction) toolResult {
	if !autoExecutable(a) {
		return notExecutedResult(a)
	}
	switch a.Type {
	case actionCreateCollection:
		return executeCreateCollection(ctx, s, a)
	case actionAddField:
		return executeAddField(ctx, s, a)
	case actionDescribeOnebox:
		return executeDescribeOnebox(a)
	case actionListCollections:
		return executeListCollections(ctx, s, a)
	case actionListRecords:
		return executeListRecords(ctx, s, a)
	case actionFindRelatedRecords:
		return executeFindRelatedRecords(ctx, s, a)
	case actionListBackups:
		return executeListBackups(ctx, s, a)
	case actionGetRecentErrors:
		return executeGetRecentErrors(ctx, s, a)
	case actionGetSettingsSummary:
		return executeGetSettingsSummary(s, a)
	default:
		// autoExecutable only ever returns true for the four cases above —
		// unreachable in practice. Fail closed (report not-executed) rather
		// than silently do nothing while claiming otherwise if that
		// invariant is ever broken by a future edit to autoExecutable.
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Not executed — no execution handler registered for %q.", a.Type)}
	}
}

// oneboxDescription backs the describe_onebox tool, offered only on the
// authenticated admin chat path — deliberately a separate, richer
// description than publicChatSystemPrompt's own (chatbot_handlers.go)
// rather than one shared constant: the public one is intentionally
// conservative (an anonymous visitor gets API-usage basics, not a feature
// tour), while this one can freely describe admin-facing capabilities
// (the AI assistant itself, backups, import/export) a public visitor has
// no access to anyway.
const oneboxDescription = "OneBox is a single-binary backend: dynamic collections (schema-defined tables, including relation fields between collections, with a REST CRUD API, realtime subscriptions, and per-field validation — email/URL format, length, range, regex, uniqueness, defaults), email/password auth with per-collection access rules, file storage, a RAG engine (ingest PDF/TXT/MD/DOCX, then query for grounded, source-cited answers), an LLM gateway that routes chat requests to Anthropic, OpenAI, or Ollama by model name, a chat AI assistant that can read your schema/records and — with confirmation for anything destructive — create collections, add fields, and reason across relations, and backup/restore plus CSV/JSON import/export with column mapping — all running from one binary with no external services required."

func executeDescribeOnebox(a proposedAction) toolResult {
	return toolResult{Action: a, Executed: true, Content: oneboxDescription}
}

func executeListCollections(ctx context.Context, s *Server, a proposedAction) toolResult {
	cols, err := listCollections(ctx, s.db)
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to list collections: %v", err)}
	}
	if len(cols) == 0 {
		return toolResult{Action: a, Executed: true, Content: "This workspace has no collections yet."}
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = fmt.Sprintf("%s (%d field(s), %d record(s))", c.Name, len(c.Schema.Fields), c.RecordCount)
	}
	return toolResult{Action: a, Executed: true, Content: "Collections: " + strings.Join(names, "; ")}
}

// listRecordsDefaultLimit/listRecordsMaxLimit bound how many records one
// list_records call pulls into the prompt — "Keep token usage low" per
// the Milestone 5 spec, and the model can always call it again (a second
// round, or a later page) if it genuinely needs more than one batch.
const (
	listRecordsDefaultLimit = 20
	listRecordsMaxLimit     = 50
)

func executeListRecords(ctx context.Context, s *Server, a proposedAction) toolResult {
	p, ok := a.Payload.(listRecordsPayload)
	if !ok {
		return toolResult{Action: a, Executed: false, Content: "Not executed — internal error reading this proposal's payload."}
	}
	c, err := getCollectionByName(ctx, s.db, p.Collection)
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to read records from %q: %v", p.Collection, err)}
	}
	limit := p.Limit
	if limit <= 0 {
		limit = listRecordsDefaultLimit
	}
	if limit > listRecordsMaxLimit {
		limit = listRecordsMaxLimit
	}
	// listRecords is the exact same data-layer function
	// GET /api/collections/:name/records calls (see record_handlers.go) —
	// no parallel query path for the model to see something a real API
	// caller couldn't.
	recs, err := listRecords(ctx, s.db, c, recordListParams{limit: limit, descending: true})
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to read records from %q: %v", p.Collection, err)}
	}
	if len(recs) == 0 {
		return toolResult{Action: a, Executed: true, Content: fmt.Sprintf("Collection %q has no records yet.", p.Collection)}
	}
	lines := make([]string, len(recs))
	for i, rec := range recs {
		lines[i] = describeRecordJSON(p.Collection, rec)
	}
	note := ""
	if c.RecordCount > len(recs) {
		note = fmt.Sprintf(" — a sample, not all %d", c.RecordCount)
	}
	return toolResult{
		Action:   a,
		Executed: true,
		Content:  fmt.Sprintf("%d record(s) from %q%s:\n%s", len(recs), p.Collection, note, strings.Join(lines, "\n")),
	}
}

// executeFindRelatedRecords backs find_related_records — the Milestone 6
// relation-traversal tool. Given one starting record, it reports two
// things, reusing only existing data-layer functions (no parallel query
// path): forward, this record's own relation fields resolved to the
// record each one points at (getCollectionByName + getRecord, the exact
// pair validateRelationValues also uses at write time); and reverse, every
// OTHER collection with a relation field that points AT the starting
// collection, filtered with listRecords' own exact-match filters
// (recordListParams.filters) to just the rows whose relation field equals
// this record's id — the same filter mechanism GET .../records?filter=
// already exposes over HTTP, not a new one invented for this tool.
func executeFindRelatedRecords(ctx context.Context, s *Server, a proposedAction) toolResult {
	p, ok := a.Payload.(findRelatedRecordsPayload)
	if !ok {
		return toolResult{Action: a, Executed: false, Content: "Not executed — internal error reading this proposal's payload."}
	}
	c, err := getCollectionByName(ctx, s.db, p.Collection)
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to find related records: collection %q: %v", p.Collection, err)}
	}
	rec, err := getRecord(ctx, s.db, c, p.RecordID)
	if err != nil {
		return toolResult{Action: a, Executed: true, Content: fmt.Sprintf("Record %q was not found in %q — nothing to find related records for.", p.RecordID, p.Collection)}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Relationships for record %s in %q:\n", p.RecordID, p.Collection)

	var forwardCount int
	for _, f := range c.Schema.Fields {
		if f.Type != FieldRelation {
			continue
		}
		v, _ := rec[f.Name].(string)
		if v == "" {
			continue
		}
		target, err := getCollectionByName(ctx, s.db, f.RelationCollection)
		if err != nil {
			fmt.Fprintf(&b, "- %s -> %q in %q — that collection no longer exists.\n", f.Name, v, f.RelationCollection)
			continue
		}
		targetRec, err := getRecord(ctx, s.db, target, v)
		if err != nil {
			fmt.Fprintf(&b, "- %s -> record %q in %q — not found (it may have been deleted).\n", f.Name, v, f.RelationCollection)
			continue
		}
		fmt.Fprintf(&b, "- %s points to %s\n", f.Name, describeRecordJSON(f.RelationCollection, targetRec))
		forwardCount++
	}
	if forwardCount == 0 {
		b.WriteString("- (no relation fields with a value on this record)\n")
	}

	collections, err := listCollections(ctx, s.db)
	if err != nil {
		fmt.Fprintf(&b, "(couldn't scan other collections for reverse references: %v)\n", err)
		return toolResult{Action: a, Executed: true, Content: b.String()}
	}
	var reverseCount int
	for _, other := range collections {
		for _, f := range other.Schema.Fields {
			if f.Type != FieldRelation || f.RelationCollection != p.Collection {
				continue
			}
			refs, err := listRecords(ctx, s.db, other, recordListParams{
				filters: map[string]string{f.Name: p.RecordID}, limit: listRecordsDefaultLimit, descending: true,
			})
			if err != nil || len(refs) == 0 {
				continue
			}
			fmt.Fprintf(&b, "- %d record(s) in %q reference this record via field %q:\n", len(refs), other.Name, f.Name)
			for _, r := range refs {
				fmt.Fprintf(&b, "    %s\n", describeRecordJSON(other.Name, r))
			}
			reverseCount++
		}
	}
	if reverseCount == 0 {
		b.WriteString("- (no other collection's records point back at this one)\n")
	}

	return toolResult{Action: a, Executed: true, Content: b.String()}
}

// executeListBackups backs list_backups (RC4) — reuses listBackupHistory,
// the exact function GET /api/backups calls, so the model sees the same
// history the Backups dashboard page shows. Read-only.
func executeListBackups(ctx context.Context, s *Server, a proposedAction) toolResult {
	items, err := listBackupHistory(ctx, s.db)
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to list backups: %v", err)}
	}
	if len(items) == 0 {
		return toolResult{Action: a, Executed: true, Content: "No backups exist yet for this workspace."}
	}
	lines := make([]string, len(items))
	for i, b := range items {
		lines[i] = fmt.Sprintf("- %s: trigger=%s status=%s size=%d bytes tables=%d created=%s", b.Filename, b.Trigger, b.Status, b.SizeBytes, b.TablesCount, b.Created)
		if b.Error != "" {
			lines[i] += " error=" + b.Error
		}
	}
	return toolResult{Action: a, Executed: true, Content: fmt.Sprintf("%d backup(s), newest first:\n%s", len(items), strings.Join(lines, "\n"))}
}

// getRecentErrorsLimit bounds how many failed requests executeGetRecentErrors
// reports — same reasoning as listRecordsDefaultLimit: keep token usage low,
// this is meant to ground a "what's going wrong" question, not dump the
// entire log.
const getRecentErrorsLimit = 10

// executeGetRecentErrors backs get_recent_errors (RC4) — reuses listLogs,
// the exact function the Logs dashboard page calls, filtered to 4xx/5xx the
// same way describeWorkspace's own "logs" page-context case already does
// (chatbot_context.go) — this tool just makes that same view reachable
// regardless of which page the admin is actually on. Read-only.
func executeGetRecentErrors(ctx context.Context, s *Server, a proposedAction) toolResult {
	entries, err := listLogs(ctx, s.db, 0, "")
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to read logs: %v", err)}
	}
	var lines []string
	for _, e := range entries {
		if e.Status < 400 {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s %s -> %d at %s", e.Method, e.Path, e.Status, e.Time))
		if len(lines) >= getRecentErrorsLimit {
			break
		}
	}
	if len(lines) == 0 {
		return toolResult{Action: a, Executed: true, Content: "No recent request errors."}
	}
	return toolResult{Action: a, Executed: true, Content: fmt.Sprintf("Recent request errors (newest first):\n%s", strings.Join(lines, "\n"))}
}

// executeGetSettingsSummary backs get_settings_summary (RC4) — the same
// safe subset describeWorkspace's own "settings" page-context case already
// exposes (provider/model names, whether a router is configured; never a
// raw API key), plus the RC3 backup-scheduler config, made reachable
// regardless of which page the admin is on. Read-only; s.providers.Load()
// is the exact same atomic snapshot every chat/RAG request already reads.
func executeGetSettingsSummary(s *Server, a proposedAction) toolResult {
	bundle := s.providers.Load()
	var b strings.Builder
	fmt.Fprintf(&b, "Chat provider: %s, model: %s (LLM router configured: %v)\n", bundle.chat.Provider, orNone(bundle.chat.Model), bundle.llm != nil)
	fmt.Fprintf(&b, "Embedding provider configured: %v\n", bundle.embedding != nil)
	if s.cfg.BackupIntervalHours > 0 {
		fmt.Fprintf(&b, "Scheduled backups: every %d hour(s), keeping the newest %d\n", s.cfg.BackupIntervalHours, s.cfg.BackupRetentionCount)
	} else {
		b.WriteString("Scheduled backups: not configured (manual backups only)\n")
	}
	return toolResult{Action: a, Executed: true, Content: b.String()}
}

func executeCreateCollection(ctx context.Context, s *Server, a proposedAction) toolResult {
	p, ok := a.Payload.(createCollectionPayload)
	if !ok {
		return toolResult{Action: a, Executed: false, Content: "Not executed — internal error reading this proposal's payload."}
	}
	fields := make([]Field, len(p.Fields))
	for i, f := range p.Fields {
		fields[i] = Field{Name: f.Name, Type: FieldType(f.Type), Required: f.Required, RelationCollection: f.RelationCollection, Validation: f.Validation}
	}
	// p.Rules is the zero value (every RuleKind "") when the model didn't
	// propose access control — createCollection's own fillDefaultRules
	// then applies the same safe defaults a real API request without
	// explicit rules gets (RC3: previously always Rules{} here, since the
	// model had no channel to propose custom rules at all).
	created, err := createCollection(ctx, s.db, p.Name, Schema{Fields: fields}, p.Rules)
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to create collection %q: %v", p.Name, err)}
	}
	content := fmt.Sprintf("Created collection %q with %d field(s): %s.", p.Name, len(p.Fields), fieldNames(p.Fields))
	content += renderSchemaAdvice(created)
	return toolResult{Action: a, Executed: true, Content: content}
}

func fieldNames(fields []proposedField) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = fmt.Sprintf("%s (%s)", f.Name, f.Type)
	}
	return strings.Join(names, ", ")
}

func executeAddField(ctx context.Context, s *Server, a proposedAction) toolResult {
	p, ok := a.Payload.(addFieldPayload)
	if !ok {
		return toolResult{Action: a, Executed: false, Content: "Not executed — internal error reading this proposal's payload."}
	}
	existing, err := getCollectionByName(ctx, s.db, p.Collection)
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to add field %q to %q: %v", p.Field, p.Collection, err)}
	}
	fields := make([]Field, 0, len(existing.Schema.Fields)+1)
	fields = append(fields, existing.Schema.Fields...)
	fields = append(fields, Field{Name: p.Field, Type: FieldType(p.Type), Required: p.Required, RelationCollection: p.RelationCollection, Validation: p.Validation})
	updated, err := updateCollectionSchema(ctx, s.db, p.Collection, Schema{Fields: fields})
	if err != nil {
		return toolResult{Action: a, Executed: false, Content: fmt.Sprintf("Failed to add field %q to %q: %v", p.Field, p.Collection, err)}
	}
	content := fmt.Sprintf("Added field %q (%s) to collection %q.", p.Field, p.Type, p.Collection)
	content += renderSchemaAdvice(updated)
	return toolResult{Action: a, Executed: true, Content: content}
}

// schemaAdvice is OneBox's own deterministic, senior-engineer schema
// review — computed by the backend, not left to the model to remember or
// improvise. RC3 found the small local model unreliable at spontaneously
// proposing this kind of thing even when asked; RC4 makes a small set of
// well-understood, low-noise patterns a guaranteed, testable backend fact
// instead — see chatbotSystemPrompt's ENGINEERING MINDSET section for the
// instruction that tells the model to relay a "Suggestion:" line verbatim
// rather than invent its own. Deliberately narrow: only fires on a field
// name strongly suggesting a well-known pattern, capped at 2 suggestions,
// never a vague "consider adding more validation."
func schemaAdvice(c *collection) []string {
	var advice []string
	for _, f := range c.Schema.Fields {
		if f.Type != FieldText {
			continue
		}
		lower := strings.ToLower(f.Name)
		hasFormat := f.Validation != nil && f.Validation.Format != ""
		hasUnique := f.Validation != nil && f.Validation.Unique

		if strings.Contains(lower, "email") && !hasFormat {
			advice = append(advice, fmt.Sprintf("Suggestion: field %q looks like an email — set validation.format=\"email\" so bad addresses are rejected at write time.", f.Name))
			if len(advice) >= 2 {
				break
			}
		}
		if looksLikeUniqueField(lower) && !hasUnique {
			advice = append(advice, fmt.Sprintf("Suggestion: field %q looks like it should be unique per record — set validation.unique=true to prevent duplicates.", f.Name))
			if len(advice) >= 2 {
				break
			}
		}
	}
	return advice
}

// looksLikeUniqueField matches field-name substrings that conventionally
// identify a record (an email, username, slug, SKU, or short code) — the
// same small, defensible set schemaAdvice's doc comment describes, not an
// attempt to guess every possible case.
func looksLikeUniqueField(lower string) bool {
	for _, hint := range []string{"email", "username", "slug", "sku", "code"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// renderSchemaAdvice formats schemaAdvice's output as a suffix to append to
// a tool result's Content — empty string (not a trailing newline) when
// there's nothing to say, so a collection with no suggestions reads
// identically to before this milestone existed.
func renderSchemaAdvice(c *collection) string {
	advice := schemaAdvice(c)
	if len(advice) == 0 {
		return ""
	}
	return "\n" + strings.Join(advice, "\n")
}

// -- the multi-round tool-execution loop ------------------------------------
//
// This is the piece that actually satisfies the AI-execution requirement
// end to end: "the model may call one or more tools, the backend executes
// them, results go back to the model, the model writes the final
// natural-language reply, and the UI never sees raw tool-call JSON." Every
// chat path (streamChatReply, writeNonStreamingReply,
// answerChatbotQuestion's non-streaming branch — see chatbot_handlers.go)
// funnels through runToolLoop instead of each hand-rolling its own
// call-parse-validate sequence, so there is exactly one place that decides
// "does this turn need another round."

// maxToolRounds bounds runToolLoop — a hard ceiling against a pathological
// back-and-forth (the model reacting to a tool result with yet another
// tool call, indefinitely), not a limit expected to be hit in ordinary use:
// a plain question resolves in round 1 with zero tool calls, and a real
// admin action resolves in round 1 (execute) + round 2 (the model reports
// back), or stays propose-only after round 1 alone when the action is
// destructive or has no execution primitive yet.
const maxToolRounds = 4

// executedActionSummary is the wire shape for one entry in
// toolLoopResult.ExecutedActions / chatbotResponse.ExecutedActions /
// streamDoneEvent.ExecutedActions — see ExecutedActions' own doc comment
// for why this carries only these three plain strings: Title is the
// fixed per-type label ("Read Records"), Description is the same
// human-sentence actionMeta.build already generates for Proposal Cards
// ("Read records from collection \"orders\"") — reused here rather than
// a second copy — and neither is the tool's raw Arguments/payload.
type executedActionSummary struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// toolLoopResult is what runToolLoop returns once a chat turn is fully
// resolved — see runToolLoop's doc comment for how each field is built.
type toolLoopResult struct {
	// Reply is the FINAL round's Content — the only text any caller may
	// ever show the admin (chatbotResponse.Reply / streamDoneEvent). Every
	// round's ToolCalls are consumed entirely inside this loop; raw
	// tool-call JSON never reaches this field, and neither does an
	// intermediate round's Content once a later round supersedes it.
	Reply string
	// Actions accumulates every proposedAction, across every round, that
	// was NOT auto-executed (destructive, or no execution primitive built
	// yet — see autoExecutable) — for the caller to render as Proposal
	// Cards. An executed action never appears here: the admin doesn't need
	// to approve something that already happened; the model's Reply is
	// expected to say so in prose instead.
	Actions []proposedAction
	// ExecutedActions accumulates a human-readable Type+Title for every
	// proposedAction that DID run for real, across every round — the AI
	// Workspace's Activity Panel (Milestone 5) renders this as "Reading
	// collection X" / "Creating collection Y", etc. Deliberately just
	// Type/Title, never Payload — the frontend must never render a
	// provider payload or raw tool-call JSON, so this struct simply
	// doesn't carry one.
	ExecutedActions []executedActionSummary
	// TokensIn/TokensOut/Timing cover every round this call actually
	// issued a provider request for. round0Result, when the caller
	// supplies one (see runToolLoop), is NOT counted here or re-logged via
	// s.logUsage — the caller already accounted for it themselves before
	// handing it in.
	TokensIn  int
	TokensOut int
	Timing    llm.ChatTiming
	// Rounds is how many provider calls this loop issued itself
	// (excluding a supplied round0Result) — diagnostic only, for callers'
	// timing logs.
	Rounds int
}

// runToolLoop is the shared core of the tool-execution pipeline. One round
// is: obtain a ChatResult (round 0 reuses round0Result if the caller
// already has one — see streamChatReply, which calls
// ChatStreamWithProvider itself first so a plain question still gets live
// token-by-token streaming; every other round calls the provider itself
// here) -> if it made no tool calls, that round's Content is the final
// answer, done -> otherwise parse the calls into candidate proposedActions
// (actionParser) -> validate them against real instance state
// (validateProposals) -> execute every auto-executable one for real
// (executeToolCall). Every one of the round's ToolCalls — including one
// that failed to parse or validate — gets exactly one matching tool-result
// message before the next round is ever sent, because a provider's API
// rejects a request containing a tool_use/tool_call with no matching
// result. If at least one call this round was actually executed, the
// assistant's exact tool calls plus every result get appended as history
// and the loop goes again (so the model can synthesize a real answer, or
// decide to call another tool); if none were (every candidate stayed
// propose-only, or every call failed validation), this round's Content is
// already the final answer — spending another round wouldn't change it.
//
// tools is threaded through by the caller (mirroring streamChatReply's own
// existing tools parameter) rather than this function defaulting to
// actionToolDefs itself — the lightweight-greeting fast path passes nil
// and must keep offering no tools at all on every round, not just round 0
// (see TestFastPathOffersNoTools); with tools==nil, a round's ToolCalls is
// always empty, so the loop resolves after round 0 exactly as before this
// milestone existed.
func (s *Server) runToolLoop(ctx context.Context, bundle *providerBundle, chat chatSelection, messages []llm.Message, tools []llm.Tool, round0Result *llm.ChatResult) (toolLoopResult, error) {
	// Copy rather than mutate the caller's slice in place — appends below
	// could otherwise silently alias/overwrite the caller's own backing
	// array if it had spare capacity.
	messages = append([]llm.Message(nil), messages...)

	var out toolLoopResult
	for round := 0; round < maxToolRounds; round++ {
		var result llm.ChatResult
		if round == 0 && round0Result != nil {
			result = *round0Result
		} else {
			r, err := bundle.llm.ChatWithProvider(ctx, chat.Provider, llm.ChatRequest{Model: chat.Model, Messages: messages, Tools: tools})
			if err != nil {
				return toolLoopResult{}, err
			}
			result = r
			s.logUsage(ctx, chat.Provider, chat.Model, result.TokensIn, result.TokensOut, false)
			out.Rounds++
		}
		out.TokensIn += result.TokensIn
		out.TokensOut += result.TokensOut
		out.Timing = result.Timing

		if len(result.ToolCalls) == 0 {
			out.Reply = result.Content
			return out, nil
		}

		candidates := actionParser.ParseActions(result)
		validated := s.validateProposals(ctx, candidates)
		validByIndex := make(map[int]proposedAction, len(validated))
		for _, a := range validated {
			validByIndex[a.CallIndex] = a
		}

		anyExecuted := false
		toolMessages := make([]llm.Message, 0, len(result.ToolCalls))
		for i, call := range result.ToolCalls {
			a, ok := validByIndex[i]
			if !ok {
				toolMessages = append(toolMessages, llm.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    fmt.Sprintf("Not executed — %q could not be validated (missing/invalid arguments, or the operation isn't currently possible against this instance). Do not claim it happened.", call.Name),
				})
				continue
			}
			tr := executeToolCall(ctx, s, a)
			if tr.Executed {
				anyExecuted = true
				out.ExecutedActions = append(out.ExecutedActions, executedActionSummary{Type: a.Type, Title: a.Title, Description: a.Description})
			} else {
				out.Actions = append(out.Actions, a)
			}
			toolMessages = append(toolMessages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: tr.Content})
		}

		if !anyExecuted {
			// If anything is pending confirmation, that's a legitimate,
			// terminal outcome — the ProposalCard UI covers it, another
			// round wouldn't change anything, and this round's own Content
			// is the model's real answer.
			if len(out.Actions) > 0 {
				out.Reply = result.Content
				return out, nil
			}
			// Nothing executed and nothing proposed either — every call
			// this round failed to even validate (malformed arguments,
			// wrong collection name, etc; toolMessages above already holds
			// one honest "Not executed — ... could not be validated"
			// explanation per call). Previously this returned immediately
			// with that feedback computed but never actually sent back to
			// the model — a real bug: the model never got the chance to
			// read its own mistake and retry (call the tool again with
			// fixed arguments, pick a different tool, or just answer in
			// prose without one), which is exactly the "tool call didn't
			// go through cleanly, try again" symptom users hit. Fixed by
			// looping again with that feedback in history, same as the
			// anyExecuted path below — still bounded by maxToolRounds, so
			// a model that keeps failing still terminates via the round-cap
			// message rather than looping forever. Genuinely nothing better
			// to try only when this round's own Content already contains a
			// real answer alongside the failed call (a model that explains
			// itself while attempting a tool call) — that's still worth
			// showing rather than silently discarding for one more retry.
			if result.Content != "" {
				out.Reply = result.Content
				return out, nil
			}
		}

		// Replay this round's assistant turn (its exact ToolCalls — real
		// IDs/Arguments, required so the tool-result messages that follow
		// have a matching call to attach to) plus every result, then go
		// again so the model can turn "the tool ran" into a real answer.
		messages = append(messages, llm.Message{Role: "assistant", Content: result.Content, ToolCalls: result.ToolCalls})
		messages = append(messages, toolMessages...)
	}

	// Hit the round cap without the model ever settling on a final,
	// tool-call-free answer — surface something honest rather than an
	// empty Reply; whatever propose-only actions accumulated along the way
	// still ride along. Two genuinely different situations here, so two
	// different honest messages: real progress (at least one tool actually
	// ran across these rounds — see the retry loop above, which now keeps
	// going specifically so a self-corrected call CAN succeed) vs. every
	// single round failing to execute anything at all, in which case
	// "I made some progress" would just be false.
	if len(out.ExecutedActions) > 0 {
		out.Reply = "I made some progress on that, but it needs another step to finish — let me know if you'd like me to continue."
	} else {
		out.Reply = "I wasn't able to complete that after a few attempts — the tool call kept failing to go through cleanly. Could you try rephrasing, or check that what you're asking about (a collection/record name, for example) actually exists?"
	}
	return out, nil
}
