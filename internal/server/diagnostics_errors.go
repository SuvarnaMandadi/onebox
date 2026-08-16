// diagnostics_errors.go turns a raw Go/HTTP error from a provider call
// into a structured diagnosticIssue — a code, a plain-language summary,
// a list of plausible causes, and concrete fixes — instead of the single
// flat string humanizeProviderError (provider_errors.go) produces. That
// function still backs the older, lightweight "Test connection" response
// shape (settings_test_connection.go) unchanged; this is the richer
// Section 4 "Live Diagnostics" version the new diagnostics panel uses,
// sharing the same underlying error-classification logic rather than
// duplicating it.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// classifyProviderError inspects a real error from an attempted provider
// call (a network error, a non-2xx HTTP response already read into
// statusCode/bodySnippet, or nil for "succeeded") and returns the
// diagnosticIssue to show for it. Every branch names a concrete,
// plausible cause list and at least one actionable fix — never a bare
// "request failed."
func classifyProviderError(err error, statusCode int, label, baseURL string) diagnosticIssue {
	retest := diagnosticFix{Label: "Re-test", Action: fixActionRetest}
	openSettings := diagnosticFix{Label: "Open provider settings", Action: fixActionOpenSettings}

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return diagnosticIssue{
				Code:     "timeout",
				Severity: severityError,
				Summary:  fmt.Sprintf("%s at %s timed out.", label, baseURL),
				PossibleCauses: []string{
					"The server is running but overloaded or slow to respond",
					"A firewall or proxy is silently dropping the connection instead of refusing it",
					"The base URL points somewhere that never responds (a dead host)",
				},
				Fixes: []diagnosticFix{retest, {Label: "Check the base URL", Action: fixActionCheckBaseURL}, openSettings},
			}
		}

		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			causes := []string{
				fmt.Sprintf("The hostname %q doesn't resolve — check for a typo in the base URL", dnsErr.Name),
				"DNS isn't reachable from wherever this OneBox instance is running",
			}
			if dnsErr.IsNotFound {
				causes = []string{fmt.Sprintf("The hostname %q doesn't exist — check for a typo in the base URL", dnsErr.Name)}
			}
			return diagnosticIssue{
				Code:           "dns_error",
				Severity:       severityError,
				Summary:        fmt.Sprintf("%s's URL (%s) doesn't resolve.", label, baseURL),
				PossibleCauses: causes,
				Fixes:          []diagnosticFix{{Label: "Check the base URL", Action: fixActionCheckBaseURL}, openSettings, retest},
			}
		}

		var opErr *net.OpError
		if errors.As(err, &opErr) || strings.Contains(strings.ToLower(err.Error()), "connection refused") || strings.Contains(strings.ToLower(err.Error()), "actively refused") {
			return diagnosticIssue{
				Code:     "connection_refused",
				Severity: severityError,
				Summary:  fmt.Sprintf("Connection refused connecting to %s at %s.", label, baseURL),
				PossibleCauses: []string{
					fmt.Sprintf("%s isn't running", label),
					"Wrong base URL (right host, wrong port — or vice versa)",
					"A firewall is blocking the port",
					"If this is a remote/VPN-only host, the VPN isn't connected",
				},
				Fixes: []diagnosticFix{
					{Label: "Check the base URL", Action: fixActionCheckBaseURL},
					openSettings,
					retest,
				},
			}
		}

		if u, perr := url.Parse(baseURL); perr == nil && u.Scheme != "http" && u.Scheme != "https" {
			return diagnosticIssue{
				Code:           "invalid_url_scheme",
				Severity:       severityError,
				Summary:        fmt.Sprintf("%q isn't a valid base URL — it must start with http:// or https://", baseURL),
				PossibleCauses: []string{"The base URL is missing its scheme, or has a typo"},
				Fixes:          []diagnosticFix{openSettings},
			}
		}

		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "certificate") || strings.Contains(lower, "x509") || strings.Contains(lower, "tls") {
			return diagnosticIssue{
				Code:     "tls_error",
				Severity: severityError,
				Summary:  fmt.Sprintf("%s's TLS certificate couldn't be verified.", label),
				PossibleCauses: []string{
					"The server is using a self-signed or expired certificate",
					"The base URL uses https:// but the server only speaks http://",
				},
				Fixes: []diagnosticFix{{Label: "Check the base URL", Action: fixActionCheckBaseURL}, openSettings},
			}
		}

		return diagnosticIssue{
			Code:           "network_error",
			Severity:       severityError,
			Summary:        fmt.Sprintf("%s: %s", label, err.Error()),
			PossibleCauses: []string{"An unexpected network-level error occurred"},
			Fixes:          []diagnosticFix{retest, openSettings},
		}
	}

	switch {
	case statusCode == 401 || statusCode == 403:
		return diagnosticIssue{
			Code:     "unauthorized",
			Severity: severityError,
			Summary:  fmt.Sprintf("%s rejected the request (HTTP %d) — the API key is missing or invalid.", label, statusCode),
			PossibleCauses: []string{
				"No API key is set",
				"The API key was revoked or mistyped",
				"The key belongs to a different account/organization than expected",
			},
			Fixes: []diagnosticFix{openSettings, retest},
		}
	case statusCode == 404:
		return diagnosticIssue{
			Code:     "not_found",
			Severity: severityError,
			Summary:  fmt.Sprintf("%s returned HTTP 404 at %s.", label, baseURL),
			PossibleCauses: []string{
				"The base URL is missing a path segment or points at the wrong host",
				"The provider's API surface changed",
			},
			Fixes: []diagnosticFix{{Label: "Check the base URL", Action: fixActionCheckBaseURL}, openSettings},
		}
	case statusCode == 429:
		return diagnosticIssue{
			Code:     "rate_limited",
			Severity: severityWarn,
			Summary:  fmt.Sprintf("%s is rate-limiting this account (HTTP 429).", label),
			PossibleCauses: []string{
				"Too many requests in a short window",
				"The account's plan/quota has been exhausted",
			},
			Fixes: []diagnosticFix{retest},
		}
	case statusCode >= 500:
		return diagnosticIssue{
			Code:           "provider_server_error",
			Severity:       severityWarn,
			Summary:        fmt.Sprintf("%s returned a server error (HTTP %d) — this is on their end, not this configuration.", label, statusCode),
			PossibleCauses: []string{"The provider is having an outage or degraded service"},
			Fixes:          []diagnosticFix{retest},
		}
	case statusCode >= 300:
		return diagnosticIssue{
			Code:     "unexpected_status",
			Severity: severityWarn,
			Summary:  fmt.Sprintf("%s responded with an unexpected HTTP %d.", label, statusCode),
			Fixes:    []diagnosticFix{retest, openSettings},
		}
	}

	// statusCode is 2xx or 0 (no HTTP response was involved) with no err —
	// unreachable in practice (callers only invoke this on a real
	// failure), but fail closed with an honest "unknown" rather than
	// panicking or fabricating a specific cause.
	return diagnosticIssue{
		Code:     "unknown_error",
		Severity: severityError,
		Summary:  fmt.Sprintf("%s: an unrecognized error occurred.", label),
		Fixes:    []diagnosticFix{retest},
	}
}

// curlCommand renders a copy-ready curl command reproducing one GET
// check — Section 9's "Copy curl command" fix. Never includes the API
// key in plaintext in a fix that might be logged or screen-shared; it
// substitutes a placeholder the operator fills in themselves.
func curlCommand(method, urlStr string, headers map[string]string, redactAuth bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -sS -X %s %q", method, urlStr)
	for k, v := range headers {
		if redactAuth && (k == "Authorization" || k == "x-api-key") {
			v = "<your API key>"
		}
		fmt.Fprintf(&b, " \\\n  -H %q", k+": "+v)
	}
	return b.String()
}
