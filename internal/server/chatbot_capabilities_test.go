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

	if got, want := strings.Join(c.FieldTypes, ", "), "bool, date, json, number, text"; got != want {
		t.Errorf("FieldTypes = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.AccessRuleKinds, ", "), "authenticated, owner, public"; got != want {
		t.Errorf("AccessRuleKinds = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.AutoColumns, ", "), "created, id, owner_id, updated"; got != want {
		t.Errorf("AutoColumns = %q, want %q", got, want)
	}
	if c.RelationsSupported || c.IndexesSupported || c.UniqueConstraintsSupported || c.AIExecutionEnabled {
		t.Errorf("expected all forward-looking flags false today, got %+v", c)
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
		"bool, date, json, number, text",
		"created, id, owner_id, updated",
		"authenticated, owner, public",
		"relationships/foreign keys",
		"custom indexes",
		"unique constraints",
		"AI execution is NOT enabled",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("describe() missing expected phrase: %q\nfull text:\n%s", phrase, got)
		}
	}
	if strings.Contains(got, "AI execution is enabled:") {
		t.Error("describe() must not claim AI execution is enabled while AIExecutionEnabled is false")
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
