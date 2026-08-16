package server

import (
	"fmt"
	"sort"
	"strings"
)

// capabilities is what the assistant is told OneBox can actually do right
// now. The previous design hardcoded these facts as prose inside
// chatbotSystemPrompt (e.g. "field types are text/number/bool/date/json
// ... no unique-constraint, index, or relation support yet") — the
// problem with that is every time the schema engine gains or drops a
// capability, a human has to remember to go find and edit that sentence
// inside a large prompt string, and nothing catches it if they forget.
//
// Instead, currentCapabilities() derives FieldTypes, AutoColumns, and
// AccessRuleKinds straight from the schema engine's own validation
// tables (validFieldTypes, systemColumns, validRuleKinds in
// collection_schema.go) — the exact same maps ValidateSchema/ValidateRules
// check requests against. Add a field type there and the assistant learns
// about it on the next request, with no prompt edit required. The
// remaining flags (RelationsSupported and friends) don't have a
// corresponding subsystem to introspect yet, so they stay explicit
// booleans here — still one single place to flip when a feature ships,
// instead of a sentence buried in prompt prose.
type capabilities struct {
	FieldTypes      []string
	AccessRuleKinds []string
	// AutoColumns are the columns every collection's table gets for free
	// (see systemColumns / createTableSQL) — the assistant must never
	// propose adding one of these itself (e.g. a redundant "created_at"
	// field when "created" already exists).
	AutoColumns []string

	RelationsSupported         bool
	IndexesSupported           bool
	UniqueConstraintsSupported bool
	// ValidationRulesSupported covers the rest of FieldValidation
	// (collection_schema.go) beyond uniqueness: format (email/url),
	// min/max length, a regex pattern, a numeric range, and a default
	// value. Shipped alongside UniqueConstraintsSupported (RC2) — kept as
	// its own flag rather than folded into that one so a future partial
	// rollback of one wouldn't silently mis-describe the other.
	ValidationRulesSupported bool
	// AIExecutionEnabled gates whether the assistant may talk about
	// actually executing a proposed action instead of only ever
	// recommending/proposing one — see proposedAction's doc comment for
	// the future approval flow this will flip on for. Stays false until
	// that flow exists; flipping it early would make the assistant claim
	// capabilities the backend doesn't have, exactly the failure mode
	// this whole type exists to prevent.
	AIExecutionEnabled bool
}

// currentCapabilities reports what this running instance actually
// supports, right now — the assistant is told to treat this as
// authoritative over anything it might otherwise assume.
func currentCapabilities() capabilities {
	return capabilities{
		FieldTypes:      sortedFieldTypes(validFieldTypes),
		AccessRuleKinds: sortedRuleKinds(validRuleKinds),
		AutoColumns:     sortedStringKeys(systemColumns),

		// True as of Milestone 6: a field can declare Type "relation" with
		// RelationCollection naming its target (see FieldType/Field in
		// collection_schema.go), and find_related_records
		// (chatbot_actions.go) lets the assistant traverse one. Still no
		// cascading deletes and no many-to-many — see describe() below for
		// exactly what that means for the assistant.
		RelationsSupported: true,
		IndexesSupported:   false,
		// True as of RC2 (checkUniqueValue/validateUniqueFields, records.go)
		// — was false when this file was first written, before that
		// feature existed; caught stale during RC3 live verification (the
		// assistant was telling admins unique constraints aren't
		// supported, weeks after they shipped).
		UniqueConstraintsSupported: true,
		ValidationRulesSupported:   true,
		// True as of the AI-execution milestone (chatbot_tool_execution.go's
		// runToolLoop/executeToolCall/autoExecutable) — safe, non-destructive
		// tool calls with a real execution primitive now run for real; see
		// this field's own doc comment and describe() below for exactly what
		// that does and does not cover.
		AIExecutionEnabled: true,
	}
}

// sortedFieldTypes and sortedRuleKinds extract and sort the keys of the
// schema engine's own validation tables (validFieldTypes, validRuleKinds
// in collection_schema.go) into a plain []string for prompt rendering —
// deliberately reading the exact same maps ValidateSchema/ValidateRules
// check requests against, so this can never drift from what's actually
// enforced.
func sortedFieldTypes(m map[FieldType]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func sortedRuleKinds(m map[RuleKind]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// sortedStringKeys is the same idea for systemColumns, a plain
// map[string]bool rather than one of the named RuleKind/FieldType maps.
func sortedStringKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// describe renders capabilities into the block prepended to every chat
// request's system prompt — admin and public alike, since none of this is
// instance-sensitive data (see describeWorkspace for what IS admin-only).
// Framed as authoritative so the model defers to it over training-data
// assumptions or something said earlier in the conversation.
func (c capabilities) describe() string {
	var b strings.Builder
	b.WriteString("CURRENT ONEBOX CAPABILITIES (authoritative — reflects this actual running instance; trust this over any assumption, including anything that sounded right earlier in this conversation):\n")
	fmt.Fprintf(&b, "- User-defined field types: %s.\n", strings.Join(c.FieldTypes, ", "))
	fmt.Fprintf(&b, "- Every collection automatically gets these columns for free: %s — never propose adding one of these yourself.\n", strings.Join(c.AutoColumns, ", "))
	fmt.Fprintf(&b, "- Access rule kinds (list/view/create/update/delete each pick exactly one): %s.\n", strings.Join(c.AccessRuleKinds, ", "))

	if c.RelationsSupported {
		b.WriteString("- Relationships ARE supported: a field can be type \"relation\", naming a target collection (see relation_collection on the field) — its value is that target's record id, checked to actually exist whenever a record is created or updated. Use the find_related_records tool to traverse one: resolve a record's relation field forward to the record it points to, or find every record elsewhere that points back at it. Still no cascading deletes and no many-to-many (a field points at exactly one collection).\n")
	}
	if c.UniqueConstraintsSupported || c.ValidationRulesSupported {
		b.WriteString("- Field validation IS supported via each field's \"validation\" object (create_collection/add_field): unique (no other record may share this value), format (email/url, text only), min_length/max_length (text), pattern (a regex, text), min/max (number), and a default value applied when a create request omits the field. Propose these whenever they genuinely fit — an email field should set format:\"email\", a field the admin calls \"unique\" or \"no duplicates\" should set unique:true — don't just describe them as good ideas without actually setting them on the field.\n")
	}

	var unsupported []string
	if !c.RelationsSupported {
		unsupported = append(unsupported, "relationships/foreign keys (workaround: store the related record's id as a plain text field — it can be migrated once relations ship)")
	}
	if !c.IndexesSupported {
		unsupported = append(unsupported, "custom indexes")
	}
	if len(unsupported) > 0 {
		fmt.Fprintf(&b, "- NOT supported yet — never describe these as configurable today: %s.\n", strings.Join(unsupported, "; "))
	}

	if c.AIExecutionEnabled {
		b.WriteString("- AI execution is enabled, split by safety: safe operations (describe OneBox, list collections, create a collection, add a field) run for real the instant you call the matching tool — trust the tool result you get back and report plainly what actually happened (or actually failed), never as a hypothetical. Potentially destructive operations (delete/drop/rename a collection, delete a field, import data, or a broader schema update) are NEVER auto-executed — calling that tool only creates a Proposal Card the admin must confirm from the dashboard; say so plainly rather than claiming it's done.\n")
	} else {
		b.WriteString("- AI execution is NOT enabled: you are advisory only. Recommend and propose in full detail, but never claim to have executed anything.\n")
	}
	return b.String()
}
