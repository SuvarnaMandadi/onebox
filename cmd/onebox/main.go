// Command onebox runs the all-in-one AI backend server.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"onebox/internal/config"
	"onebox/internal/db"
	"onebox/internal/server"
)

// version is set at build time via -ldflags "-X main.version=...", see
// scripts/build-release.sh.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		log.Println("onebox " + version)
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// warnIfDefaultJWTSecret logs one impossible-to-miss startup warning when
// ONEBOX_JWT_SECRET was never set — deliberately not a fatal error (a
// fresh local dev/CI checkout must keep working with zero configuration),
// but the stakes are higher than a normal "insecure default" because this
// one secret does double duty: it signs every admin/user session JWT AND
// (see internal/server/settings_crypto.go's settingsKey) derives the
// AES-256 key that encrypts provider API keys at rest. Left at the
// default, anyone who reads this file's own source (it's public) can
// forge an admin session and decrypt every stored provider key — so this
// prints on every single startup, not just once ever, for as long as the
// operator leaves it unset.
func warnIfDefaultJWTSecret(cfg config.Config) {
	if !cfg.JWTSecretIsDefault {
		return
	}
	log.Print(`
================================================================================
 WARNING: ONEBOX_JWT_SECRET is not set — using the default, PUBLIC secret.
================================================================================
 This is fine for local development, but this default is checked into the
 open-source onebox repository, so anyone can read it. If this instance is
 reachable by anyone other than you (staging, production, or any network
 you don't fully trust), an attacker who reads the source can:
   - forge a valid admin session JWT (i.e. log in as you, with no password)
   - decrypt every provider API key stored in Settings (the same secret
     also derives that encryption key — see settings_crypto.go)
 Set ONEBOX_JWT_SECRET to a long random value before deploying anywhere
 non-local, e.g.: ONEBOX_JWT_SECRET=$(openssl rand -hex 32)
================================================================================`)
}

func run() error {
	cfg := config.Load()
	cfg.Version = version
	log.Printf("onebox %s starting", version)
	warnIfDefaultJWTSecret(cfg)

	sqlDB, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if err := db.Migrate(sqlDB); err != nil {
		return err
	}

	srv := server.New(cfg, sqlDB)
	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Router(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("onebox listening on %s (data dir: %s)", cfg.Addr, cfg.DataDir)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}
