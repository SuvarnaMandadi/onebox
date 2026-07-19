package server

import (
	"context"
	"database/sql"
	"fmt"
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
