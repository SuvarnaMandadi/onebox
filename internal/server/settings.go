package server

import (
	"context"
	"database/sql"
	"fmt"

	"onebox/internal/config"
)

// settingKey is one of the small, fixed set of provider-config values the
// dashboard can override at runtime (see reloadProviders in server.go).
type settingKey string

const (
	settingAnthropicAPIKey settingKey = "anthropic_api_key"
	settingAnthropicModel  settingKey = "anthropic_model"

	settingOpenAIAPIKey  settingKey = "openai_api_key"
	settingOpenAIBaseURL settingKey = "openai_base_url"

	settingEmbeddingProvider settingKey = "embedding_provider"
	settingEmbeddingAPIKey   settingKey = "embedding_api_key"
	settingEmbeddingBaseURL  settingKey = "embedding_base_url"
	settingEmbeddingModel    settingKey = "embedding_model"

	settingOllamaBaseURL settingKey = "ollama_base_url"

	// settingChatProvider and settingChatModel select which backend the
	// dashboard's own chat surfaces (the admin chatbot panel and
	// /api/rag/answer) use — completely independent of
	// settingEmbeddingProvider above. One of "ollama" (default),
	// "anthropic", or "openai"; see resolveChatProvider in server.go.
	// Credentials/base URLs are NOT duplicated here — Anthropic uses
	// settingAnthropicAPIKey, OpenAI uses settingOpenAIAPIKey/BaseURL,
	// Ollama uses settingOllamaBaseURL (the same one embeddings share).
	settingChatProvider settingKey = "chat_provider"
	settingChatModel    settingKey = "chat_model"

	// settingRegistrationEnabled gates POST /api/auth/signup (regular user
	// self-registration). Stored as the string "true"/"false"; absent (a
	// fresh instance, or one upgraded from before this setting existed)
	// means enabled, so existing deployments don't silently lock out
	// signup — see registrationEnabled below.
	settingRegistrationEnabled settingKey = "registration_enabled"

	// settingChatShareToken is deliberately not in allSettingKeys — it's
	// managed only through the dedicated /api/chat-share endpoints, not
	// the generic settings PUT, so it can't be set to an arbitrary value.
	settingChatShareToken settingKey = "chat_share_token"
)

// secretSettingKeys never round-trip back to the client in plaintext once
// saved — GET /api/settings reports only whether they're set.
var secretSettingKeys = map[settingKey]bool{
	settingAnthropicAPIKey: true,
	settingOpenAIAPIKey:    true,
	settingEmbeddingAPIKey: true,
}

var allSettingKeys = []settingKey{
	settingAnthropicAPIKey, settingAnthropicModel,
	settingOpenAIAPIKey, settingOpenAIBaseURL,
	settingEmbeddingProvider, settingEmbeddingAPIKey, settingEmbeddingBaseURL, settingEmbeddingModel,
	settingOllamaBaseURL,
	settingChatProvider, settingChatModel,
	settingRegistrationEnabled,
}

func isKnownSettingKey(key settingKey) bool {
	for _, k := range allSettingKeys {
		if k == key {
			return true
		}
	}
	return false
}

func setSetting(ctx context.Context, sqlDB *sql.DB, jwtSecret string, key settingKey, value string) error {
	encrypted, err := encryptSetting(jwtSecret, value)
	if err != nil {
		return fmt.Errorf("encrypt setting %s: %w", key, err)
	}
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO _settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		key, encrypted,
	)
	if err != nil {
		return fmt.Errorf("save setting %s: %w", key, err)
	}
	return nil
}

// registrationEnabled reports whether POST /api/auth/signup should accept
// new regular-user accounts. Defaults to true (absent setting) so it's
// opt-out, not opt-in — an operator who never visits Settings keeps the
// registration behavior onebox always had.
func registrationEnabled(ctx context.Context, sqlDB *sql.DB, jwtSecret string) (bool, error) {
	stored, err := getAllSettings(ctx, sqlDB, jwtSecret)
	if err != nil {
		return false, err
	}
	v, ok := stored[settingRegistrationEnabled]
	if !ok || v == "" {
		return true, nil
	}
	return v == "true", nil
}

// chatSelection is the resolved {provider, model} the dashboard's own chat
// surfaces — the admin chatbot panel and /api/rag/answer — use for their
// next request. It has nothing to do with POST /api/llm/chat, which keeps
// taking an explicit model from the caller (see llm.ProviderKind); this is
// purely what those two internal call sites use when onebox itself has to
// pick a model on the operator's behalf.
type chatSelection struct {
	Provider string // "ollama", "anthropic", or "openai"
	Model    string
}

// resolveChatSelection computes the effective chat provider/model: an
// explicit chat_provider/chat_model setting (saved from the dashboard's
// Chat Provider panel) wins, then cfg's ChatProvider/ChatModel (env vars,
// ONEBOX_CHAT_PROVIDER/ONEBOX_CHAT_MODEL), then "ollama" with no model —
// deliberately not Anthropic. Before this, both the admin chatbot and
// /api/rag/answer were hardcoded to cfg.AnthropicModel regardless of
// whether an Anthropic key was even configured, which is exactly the bug
// this replaces: a self-hoster running Ollama-only, with no Anthropic key
// at all, got "anthropic API error: invalid x-api-key" instead of a
// working chat. Ollama needs no key and is what the embedding provider
// already defaults to, so it's the safe out-of-the-box choice; anyone who
// wants Claude or an OpenAI-compatible backend now picks it explicitly in
// Settings → Chat Provider.
func resolveChatSelection(cfg config.Config, stored map[settingKey]string) chatSelection {
	provider := stored[settingChatProvider]
	if provider == "" {
		provider = cfg.ChatProvider
	}
	if provider == "" {
		provider = "ollama"
	}

	model := stored[settingChatModel]
	if model == "" {
		model = cfg.ChatModel
	}
	if model == "" {
		switch provider {
		case "anthropic":
			// Falls back through the legacy anthropic_model setting/env so
			// a deployment that configured Anthropic before this refactor
			// (and now explicitly opts chat_provider into "anthropic")
			// doesn't need to also re-enter its model choice.
			if v := stored[settingAnthropicModel]; v != "" {
				model = v
			} else {
				model = cfg.AnthropicModel
			}
		case "openai":
			model = "gpt-4o-mini"
		}
		// Ollama intentionally has no universal default model — installed
		// models vary per machine, so an empty selection surfaces as a
		// clear "choose a model in Settings" error rather than a guess
		// that's likely wrong (see handleChatbot/handleRAGAnswer).
	}

	return chatSelection{Provider: provider, Model: model}
}

// getAllSettings decrypts every stored setting. Missing keys are simply
// absent from the map (callers overlay onto env-based defaults).
func getAllSettings(ctx context.Context, sqlDB *sql.DB, jwtSecret string) (map[settingKey]string, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT key, value FROM _settings`)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()

	out := make(map[settingKey]string)
	for rows.Next() {
		var key, encrypted string
		if err := rows.Scan(&key, &encrypted); err != nil {
			return nil, fmt.Errorf("scan setting row: %w", err)
		}
		decrypted, err := decryptSetting(jwtSecret, encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt setting %s: %w", key, err)
		}
		out[settingKey(key)] = decrypted
	}
	return out, rows.Err()
}
