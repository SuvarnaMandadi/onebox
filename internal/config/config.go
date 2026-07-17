// Package config loads onebox server configuration from environment
// variables, with sane defaults for local development.
package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// DataDir holds the SQLite database, uploaded files, and other
	// persistent state.
	DataDir string
	// DBPath is the path to the main SQLite database file.
	DBPath string
	// JWTSecret signs auth session tokens.
	JWTSecret string
}

// Load builds a Config from environment variables, falling back to
// development defaults for anything unset.
func Load() Config {
	dataDir := getEnv("ONEBOX_DATA_DIR", "./onebox_data")
	secret := getEnv("ONEBOX_JWT_SECRET", "dev-insecure-secret-change-me")

	return Config{
		Addr:      getEnv("ONEBOX_ADDR", ":8090"),
		DataDir:   dataDir,
		DBPath:    filepath.Join(dataDir, "data.db"),
		JWTSecret: secret,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
