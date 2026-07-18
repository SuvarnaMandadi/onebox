package server

import (
	"encoding/json"
	"net/http"
)

// handleGetSettings returns every known setting's current value — except
// secret keys (API keys), which only report whether they're set. Secrets
// never round-trip back to the browser in plaintext once saved.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	stored, err := getAllSettings(r.Context(), s.db, s.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load settings", nil)
		return
	}

	out := make(map[string]any, len(allSettingKeys))
	for _, key := range allSettingKeys {
		v, has := stored[key]
		if secretSettingKeys[key] {
			out[string(key)] = map[string]bool{"set": has && v != ""}
		} else {
			out[string(key)] = v
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateSettings accepts a partial {key: value} object — only keys
// present in the body are changed — validates every key is recognized,
// persists them encrypted, and hot-reloads the live provider clients so
// the change applies immediately.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be a JSON object of key: value", nil)
		return
	}

	for key := range body {
		if !isKnownSettingKey(settingKey(key)) {
			writeError(w, http.StatusBadRequest, "unknown_setting", "unknown setting key: "+key, nil)
			return
		}
	}

	for key, value := range body {
		if err := setSetting(r.Context(), s.db, s.cfg.JWTSecret, settingKey(key), value); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to save setting "+key, nil)
			return
		}
	}

	if err := s.reloadProviders(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "settings saved but failed to reload providers: "+err.Error(), nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
