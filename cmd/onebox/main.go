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

func run() error {
	cfg := config.Load()
	log.Printf("onebox %s starting", version)

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
