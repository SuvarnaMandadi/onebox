package server

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// humanizeProviderError turns a raw Go/HTTP client error from an
// embedding or LLM provider call into a plain-language explanation,
// naming the likely cause instead of a generic "request failed". Used
// both by RAG ingestion failures (shown on the source's status) and by
// the Settings page's "Test connection" button.
func humanizeProviderError(err error, label, baseURL string) string {
	if err == nil {
		return ""
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return fmt.Sprintf("%s isn't reachable at %s — is it running? (Settings → Providers)", label, baseURL)
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "actively refused"):
		return fmt.Sprintf("%s isn't reachable at %s — is it running? (Settings → Providers)", label, baseURL)
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "server misbehaving"):
		return fmt.Sprintf("%s's URL looks wrong (%s) — check it in Settings → Providers", label, baseURL)
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid api key") || strings.Contains(lower, "invalid_api_key") || strings.Contains(lower, "authentication"):
		return fmt.Sprintf("%s rejected the API key — check it in Settings → Providers", label)
	case strings.Contains(lower, "404") || (strings.Contains(lower, "model") && strings.Contains(lower, "not found")):
		return fmt.Sprintf("%s model not found — check the model name in Settings → Providers", label)
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "context deadline"):
		return fmt.Sprintf("%s at %s timed out — is it running and reachable?", label, baseURL)
	default:
		return fmt.Sprintf("%s: %s", label, msg)
	}
}
