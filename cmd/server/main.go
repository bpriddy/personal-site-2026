// Command server runs the personal site: public pages (the HTML transcript plus
// the front-end host, which embeds a front end from the user-content service),
// the /api endpoints, and the /admin CMS.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/server"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load(config.Main)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	var st store.Store
	if cfg.DatabaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		pg, err := store.OpenPostgres(ctx, cfg.DatabaseURL) // also applies migrations
		cancel()
		if err != nil {
			log.Error("database", "err", err)
			os.Exit(1)
		}
		defer pg.Close()
		st = pg
		log.Info("store: postgres")
	} else {
		st = store.NewMemory() // dev only; config requires DATABASE_URL in prod
		log.Warn("store: in-memory (DATABASE_URL unset); edits are lost on restart")
	}

	srv, err := server.New(cfg, st, log)
	if err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Cloud Run sends SIGTERM before stopping an instance
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", httpSrv.Addr, "env", cfg.Env)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}
