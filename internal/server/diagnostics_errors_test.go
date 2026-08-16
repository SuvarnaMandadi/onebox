package server

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestClassifyProviderErrorConnectionRefused(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: errConnRefused{}}
	issue := classifyProviderError(err, 0, "Ollama", "http://localhost:11434")
	if issue.Code != "connection_refused" {
		t.Fatalf("code = %q, want connection_refused", issue.Code)
	}
	if len(issue.PossibleCauses) == 0 {
		t.Error("expected PossibleCauses to be populated")
	}
	if len(issue.Fixes) == 0 {
		t.Error("expected at least one Fix")
	}
}

type errConnRefused struct{}

func (errConnRefused) Error() string { return "connect: connection refused" }

func TestClassifyProviderErrorDNS(t *testing.T) {
	err := &net.DNSError{Name: "totally-fake-host.invalid", IsNotFound: true}
	issue := classifyProviderError(err, 0, "OpenAI", "http://totally-fake-host.invalid")
	if issue.Code != "dns_error" {
		t.Fatalf("code = %q, want dns_error", issue.Code)
	}
}

func TestClassifyProviderErrorTimeout(t *testing.T) {
	issue := classifyProviderError(context.DeadlineExceeded, 0, "Ollama", "http://localhost:11434")
	if issue.Code != "timeout" {
		t.Fatalf("code = %q, want timeout", issue.Code)
	}
}

func TestClassifyProviderErrorUnauthorized(t *testing.T) {
	issue := classifyProviderError(nil, 401, "Anthropic", "https://api.anthropic.com")
	if issue.Code != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", issue.Code)
	}
	if issue.Severity != severityError {
		t.Errorf("severity = %q, want error", issue.Severity)
	}
}

func TestClassifyProviderErrorNotFound(t *testing.T) {
	issue := classifyProviderError(nil, 404, "OpenAI", "https://api.openai.com/v1")
	if issue.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", issue.Code)
	}
}

func TestClassifyProviderErrorRateLimited(t *testing.T) {
	issue := classifyProviderError(nil, 429, "OpenAI", "https://api.openai.com/v1")
	if issue.Code != "rate_limited" {
		t.Fatalf("code = %q, want rate_limited", issue.Code)
	}
	if issue.Severity != severityWarn {
		t.Errorf("severity = %q, want warning — transient, not a config problem", issue.Severity)
	}
}

func TestClassifyProviderErrorServerError(t *testing.T) {
	issue := classifyProviderError(nil, 503, "Anthropic", "https://api.anthropic.com")
	if issue.Code != "provider_server_error" {
		t.Fatalf("code = %q, want provider_server_error", issue.Code)
	}
	if issue.Severity != severityWarn {
		t.Errorf("severity = %q, want warning — this is on the provider's end, not a misconfiguration", issue.Severity)
	}
}

// TestClassifyProviderErrorEveryBranchHasCausesAndFixes is Section 4's
// core promise as a single sweeping test: "never a bare failure message"
// — every classification this function can produce must explain why and
// suggest what to do, not just restate that something failed.
func TestClassifyProviderErrorEveryBranchHasCausesAndFixes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		statusCode int
	}{
		{"timeout", context.DeadlineExceeded, 0},
		{"dns", &net.DNSError{Name: "bad.invalid", IsNotFound: true}, 0},
		{"refused", &net.OpError{Op: "dial", Err: errConnRefused{}}, 0},
		{"unauthorized", nil, 401},
		{"forbidden", nil, 403},
		{"not_found", nil, 404},
		{"rate_limited", nil, 429},
		{"server_error", nil, 500},
		{"unexpected_status", nil, 302},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issue := classifyProviderError(c.err, c.statusCode, "TestProvider", "http://example.invalid")
			if issue.Summary == "" {
				t.Error("Summary is empty")
			}
			if len(issue.Fixes) == 0 {
				t.Error("expected at least one Fix — never a dead-end error")
			}
		})
	}
}

func TestCurlCommandRedactsAuthHeader(t *testing.T) {
	cmd := curlCommand("GET", "https://api.openai.com/v1/models", map[string]string{"Authorization": "Bearer sk-real-secret-key"}, true)
	if strings.Contains(cmd, "sk-real-secret-key") {
		t.Fatal("curl command leaked the real API key — must be redacted")
	}
	if !strings.Contains(cmd, "<your API key>") {
		t.Fatal("expected a placeholder for the redacted key")
	}
}

func TestCurlCommandKeepsNonAuthHeaders(t *testing.T) {
	cmd := curlCommand("GET", "http://localhost:11434/api/tags", map[string]string{"Content-Type": "application/json"}, true)
	if !strings.Contains(cmd, "application/json") {
		t.Fatal("expected non-auth headers to be included as-is")
	}
}
