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
	"strconv"
	"syscall"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/llm"
	"github.com/bpriddy/personal-site-2026/internal/observer"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
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
	var obsStore store.ObserverStore
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
		obsStore = pg.Observer()
		log.Info("store: postgres")
	} else {
		st = store.NewMemory() // dev only; config requires DATABASE_URL in prod
		obsStore = store.NewObserverMemory()
		log.Warn("store: in-memory (DATABASE_URL unset); edits are lost on restart")
	}

	// builder: revision files in FRONTENDS_BUCKET (prod) or FRONTENDS_DIR/rev
	// (dev); chat needs ANTHROPIC_API_KEY
	var files revfiles.Store = revfiles.NewDir(cfg.FrontendsDir)
	if cfg.FrontendsBucket != "" {
		gcs, err := revfiles.NewGCS(context.Background(), cfg.FrontendsBucket)
		if err != nil {
			log.Error("frontends bucket", "err", err)
			os.Exit(1)
		}
		files = gcs
		log.Info("revisions: cloud storage", "bucket", cfg.FrontendsBucket)
	}
	var agent *builder.Builder
	if client := llm.NewClient(cfg.AnthropicAPIKey); client != nil {
		agent = builder.New(builder.ClaudeModel{Client: client}, builder.Config{Model: llm.Model, Fallbacks: true})
	} else {
		log.Warn("builder: ANTHROPIC_API_KEY unset; builder chat disabled")
	}

	// Cloud Run sends SIGTERM before stopping an instance
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obs := newObserver(cfg, st, obsStore, log)
	obs.Start(ctx)

	srv, err := server.New(cfg, st, log, server.WithObserver(obs), server.WithBuilder(agent, files))
	if err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

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

// newObserver builds the site observer. Without ANTHROPIC_API_KEY it still
// records detections, but healing (generation) is off.
func newObserver(cfg config.Config, st store.Store, obsStore store.ObserverStore, log *slog.Logger) *observer.Observer {
	oc := observer.Config{Content: st, Obs: obsStore, ModelName: llm.Model, Log: log, XFFHops: 1}
	// X-Forwarded-For entries appended by Google's front ends: 1 on Cloud Run
	// alone, 2 behind the external load balancer (see observer.ClientIP)
	if v, err := strconv.Atoi(os.Getenv("OBSERVE_XFF_HOPS")); err == nil {
		oc.XFFHops = v
	}
	if client := llm.NewClient(cfg.AnthropicAPIKey); client != nil {
		oc.Model = &observer.Claude{Client: client, Model: llm.Model}
	} else {
		log.Warn("observer: ANTHROPIC_API_KEY unset; detections are recorded but not healed")
	}
	return observer.New(oc)
}
