package server

import (
	"encoding/json"
	"net/http"
)

// errorEnvelope is the consistent JSON error shape returned by every API
// endpoint, per the blueprint's API design rules.
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, errorEnvelope{Code: code, Message: message, Details: details})
}
