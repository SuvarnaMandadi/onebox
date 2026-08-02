package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"onebox/internal/llm"
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

	if err := validateChatModel(r.Context(), s.db, s.cfg.JWTSecret, body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_chat_model", err.Error(), nil)
		return
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

// validateChatModel is a server-side guard against the exact bug reported
// against the Chat Provider panel: an embedding-only Ollama model (like
// nomic-embed-text) ending up saved as the dashboard's own chat selection.
// The dashboard's JS already keeps this from happening in normal use (see
// app.js's modelByProvider/renderChatFields), but settings are a plain
// {key: value} PUT with no schema enforcement beyond isKnownSettingKey, so
// this is the backstop for any other caller of the same endpoint (a script,
// an older cached page, a future UI bug) — it must be impossible to persist
// an unusable chat model, not just discouraged in the one UI that exists
// today.
//
// It only resolves the *combination* that would exist after this request:
// if the request only touches one of chat_provider/chat_model, the other
// half is read from what's already stored, so e.g. a request that only
// changes chat_model against an already-saved chat_provider=ollama is still
// checked correctly.
func validateChatModel(ctx context.Context, sqlDB *sql.DB, jwtSecret string, body map[string]string) error {
	provider, hasProvider := body[string(settingChatProvider)]
	model, hasModel := body[string(settingChatModel)]
	if !hasProvider && !hasModel {
		return nil // neither field is changing — nothing to validate
	}

	if !hasProvider || !hasModel {
		stored, err := getAllSettings(ctx, sqlDB, jwtSecret)
		if err != nil {
			return err
		}
		if !hasProvider {
			provider = stored[settingChatProvider]
		}
		if !hasModel {
			model = stored[settingChatModel]
		}
	}

	if provider == "ollama" && model != "" && llm.IsEmbeddingModel(model) {
		return fmt.Errorf("%q looks like an embedding-only Ollama model, not a chat model — pick a chat-capable model in Settings → Chat Provider (embedding models belong only to the Embedding Provider)", model)
	}
	return nil
}
