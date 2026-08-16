package server

import (
	"strings"
	"testing"
)

// TestCurrentCapabilitiesDerivesFromSchemaEngine pins down the core claim
// behind chatbot_capabilities.go: FieldTypes, AccessRuleKinds, and
// AutoColumns are read straight from the schema engine's own validation
// tables (validFieldTypes, validRuleKinds, systemColumns in
// collection_schema.go), not a separately hand-maintained list.
func TestCurrentCapabilitiesDerivesFromSchemaEngine(t *testing.T) {
	c := currentCapabilities()

	if got, want := strings.Join(c.FieldTypes, ", "), "bool, date, json, number, relation, text"; got != want {
		t.Errorf("FieldTypes = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.AccessRuleKinds, ", "), "authenticated, owner, public"; got != want {
		t.Errorf("AccessRuleKinds = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.AutoColumns, ", "), "created, id, owner_id, updated"; got != want {
		t.Errorf("AutoColumns = %q, want %q", got, want)
	}
	// RelationsSupported flips true as of Milestone 6 (see FieldRelation,
	// collection_schema.go). UniqueConstraintsSupported/ValidationRulesSupported
	// flip true as of RC2 (FieldValidation, same file) — indexes remain
	// unbuilt.
	if !c.RelationsSupported {
		t.Errorf("expected RelationsSupported true now that relation fields exist, got %+v", c)
	}
	if !c.UniqueConstraintsSupported || !c.ValidationRulesSupported {
		t.Errorf("expected unique-constraints/validation-rules flags true now that FieldValidation exists, got %+v", c)
	}
	if c.IndexesSupported {
		t.Errorf("expected IndexesSupported false — custom indexes genuinely aren't built, got %+v", c)
	}
	// AIExecutionEnabled is true as of the AI-execution milestone (see
	// runToolLoop/executeToolCall in chatbot_tool_execution.go) — safe
	// tool calls now run for real, unlike the still-unbuilt schema
	// features above.
	if !c.AIExecutionEnabled {
		t.Errorf("expected AIExecutionEnabled true now that runToolLoop executes safe actions for real, got %+v", c)
	}
}

// TestCurrentCapabilitiesTracksSchemaEngineChanges is the actual point of
// deriving from the schema engine's validation tables instead of a
// hardcoded prompt sentence: adding a field type there must be visible
// here immediately, with zero prompt edits required.
func TestCurrentCapabilitiesTracksSchemaEngineChanges(t *testing.T) {
	validFieldTypes["vector"] = true
	defer delete(validFieldTypes, "vector")

	c := currentCapabilities()
	found := false
	for _, ft := range c.FieldTypes {
		if ft == "vector" {
			found = true
		}
	}
	if !found {
		t.Fatalf("FieldTypes = %v, want it to include a newly-added schema-engine field type with no code change here", c.FieldTypes)
	}
}

func TestCapabilitiesDescribeContent(t *testing.T) {
	got := currentCapabilities().describe()

	mustContain := []string{
		"CURRENT ONEBOX CAPABILITIES",
		"bool, date, json, number, relation, text",
		"created, id, owner_id, updated",
		"authenticated, owner, public",
		"Relationships ARE supported",
		"find_related_records",
		"custom indexes",
		// RC3: unique constraints and the rest of FieldValidation (format/
		// length/pattern/range/default) shipped in RC2 — this pin used to
		// require the OLD "NOT supported yet: unique constraints" text,
		// which was stale and actively wrong by the time RC3 caught it
		// live (the assistant was telling admins a real, working feature
		// didn't exist). Now asserts the corrected, supported framing.
		"Field validation IS supported",
		"unique (no other record may share this value)",
		"AI execution is enabled",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("describe() missing expected phrase: %q\nfull text:\n%s", phrase, got)
		}
	}
	if strings.Contains(got, "AI execution is NOT enabled") {
		t.Error("describe() must not claim AI execution is disabled now that AIExecutionEnabled is true")
	}
	if strings.Contains(got, "relationships/foreign keys") {
		t.Error("describe() must not list relationships as unsupported now that RelationsSupported is true")
	}
	if strings.Contains(got, "unique constraints (call this out") {
		t.Error("describe() must not list unique constraints as unsupported — UniqueConstraintsSupported is true as of RC2")
	}
}

func TestCapabilitiesDescribeReflectsEnabledExecution(t *testing.T) {
	c := currentCapabilities()
	c.AIExecutionEnabled = true
	got := c.describe()
	if !strings.Contains(got, "AI execution is enabled") {
		t.Error("describe() must reflect AIExecutionEnabled=true when set")
	}
	if strings.Contains(got, "AI execution is NOT enabled") {
		t.Error("describe() must not show the disabled-execution text when AIExecutionEnabled is true")
	}
}
