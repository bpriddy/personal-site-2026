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
	"strings"
	"syscall"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/connect"
	"github.com/bpriddy/personal-site-2026/internal/llm"
	"github.com/bpriddy/personal-site-2026/internal/media"
	"github.com/bpriddy/personal-site-2026/internal/notify"
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
	// media (/media/...): FRONTENDS_BUCKET objects media/..., else MEDIA_DIR
	mediaSrc, err := media.FromEnv(context.Background(), cfg.FrontendsBucket, cfg.MediaDir)
	if err != nil {
		log.Error("media", "err", err)
		os.Exit(1)
	}
	// the builder's connections to outside services (internal/connect)
	conns := connections(mediaSrc, cfg.UsercontentOrigin, log)
	var agent *builder.Builder
	if cfg.Dev() && os.Getenv("BUILDER_DEMO_MODEL") == "1" {
		// offline: a canned model for local work and the e2e tests (never in prod)
		delay := 300 * time.Millisecond
		if d, err := time.ParseDuration(os.Getenv("BUILDER_DEMO_DELAY")); err == nil && d >= 0 {
			delay = d // e.g. 3s, to look at the streaming UI
		}
		agent = builder.New(&builder.DemoModel{Delay: delay}, builder.Config{Model: "demo"})
		log.Warn("builder: BUILDER_DEMO_MODEL=1; using the offline demo model, not Claude")
	} else if client := llm.NewClient(cfg.AnthropicAPIKey); client != nil {
		agent = builder.New(builder.ClaudeModel{Client: client}, builder.Config{Model: llm.Model, Fallbacks: true, Connections: conns})
	} else {
		log.Warn("builder: ANTHROPIC_API_KEY unset; builder chat (admin and public) disabled")
	}
	// the public builder's limits (BUILD_*; client IPs as the observer counts them)
	limits, err := server.BuildLimitsFromEnv(os.Getenv)
	if err != nil {
		log.Error("build limits", "err", err)
		os.Exit(1)
	}

	// the builder's spend limits (BUILD_BUDGET_*; Ben can change them in the admin)
	budget, err := server.BudgetFromEnv(os.Getenv)
	if err != nil {
		log.Error("build budget", "err", err)
		os.Exit(1)
	}

	// Cloud Run sends SIGTERM before stopping an instance
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obs := newObserver(cfg, st, obsStore, files, log)

	srv, err := server.New(cfg, st, log, server.WithObserver(obs), server.WithBuilder(agent, files),
		server.WithBuildLimits(limits), server.WithBudget(budget), server.WithMedia(mediaSrc),
		server.WithMailer(newMailer(cfg, log)))
	if err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}
	// content drift: the observer rebuilds front ends with the builder agent
	// (the server runs it); without a model, drift is only recorded
	if agent != nil {
		obs.SetRebuilder(srv)
	}
	obs.Start(ctx)

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
	srv.InterruptRuns(shutdownCtx) // builds in progress die with this instance: don't leave them "running"
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}

// newObserver builds the site observer. Without ANTHROPIC_API_KEY it still
// records detections, but healing (generation) is off.
func newObserver(cfg config.Config, st store.Store, obsStore store.ObserverStore, files revfiles.Store, log *slog.Logger) *observer.Observer {
	oc := observer.Config{Content: st, Obs: obsStore, ModelName: llm.Model, Log: log, XFFHops: 1, Files: files}
	// creative rebuilds for content drift, per rolling 24 hours (all instances)
	if v, err := strconv.Atoi(os.Getenv("OBSERVER_REBUILDS_PER_DAY")); err == nil && v > 0 {
		oc.RebuildsPerDay = v
	}
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

// connections are the builder's outside services. BUILDER_CONNECTIONS lists
// them (default: all); Sketchfab needs SKETCHFAB_API_TOKEN, Krea KREA_API_KEY. They import
// into the media store, so it must be writable.
func connections(src media.Source, publicOrigin string, log *slog.Logger) []connect.Connection {
	st, ok := src.(connect.Store)
	if !ok {
		log.Warn("builder: the media store isn't writable; no connections")
		return nil
	}
	want := map[string]bool{"sketchfab": true, "polyhaven": true, "googlefonts": true, "krea": true, "kreavideo": true}
	if v, set := os.LookupEnv("BUILDER_CONNECTIONS"); set {
		want = map[string]bool{}
		for _, n := range strings.Split(v, ",") {
			want[strings.TrimSpace(n)] = true
		}
	}
	var out []connect.Connection
	if want["sketchfab"] {
		if tok := os.Getenv("SKETCHFAB_API_TOKEN"); tok != "" {
			out = append(out, &connect.Sketchfab{Token: tok, Store: st})
		} else {
			log.Warn("builder: SKETCHFAB_API_TOKEN unset; no Sketchfab models")
		}
	}
	if want["krea"] {
		if tok := os.Getenv("KREA_API_KEY"); tok != "" {
			out = append(out, &connect.Krea{Token: tok, Store: st, Public: publicOrigin})
			if want["kreavideo"] {
				// Ben's runs only, under a daily cap (KREA_VIDEO_DAILY_USD, default 15)
				capUSD, _ := strconv.ParseFloat(os.Getenv("KREA_VIDEO_DAILY_USD"), 64)
				out = append(out, &connect.KreaVideo{Token: tok, Store: st, Public: publicOrigin, DailyCap: capUSD})
			}
		} else {
			log.Warn("builder: KREA_API_KEY unset; no generated images")
		}
	}
	if want["polyhaven"] {
		out = append(out, &connect.PolyHaven{Store: st})
	}
	if want["googlefonts"] {
		out = append(out, &connect.GoogleFonts{Store: st})
	}
	var names []string
	for _, c := range out {
		names = append(names, c.Name())
	}
	log.Info("builder: connections", "on", strings.Join(names, ","))
	return out
}

// newMailer is how build emails go out (protocol v1.13): SendGrid when
// SENDGRID_API_KEY is set (from secret sendgrid-api-key in prod), else in dev
// an outbox that only logs (GET /dev/outbox), else none: the offer is hidden.
func newMailer(cfg config.Config, log *slog.Logger) notify.Mailer {
	if key := os.Getenv("SENDGRID_API_KEY"); key != "" {
		from := os.Getenv("NOTIFY_FROM")
		if from == "" {
			from = "info@lanterns.build"
		}
		name := os.Getenv("NOTIFY_FROM_NAME")
		if name == "" {
			name = "benpriddy.com"
		}
		log.Info("notify: sendgrid", "from", from)
		return &notify.SendGrid{Key: key, From: from, FromName: name}
	}
	if cfg.Dev() {
		return &notify.Outbox{Log: log}
	}
	log.Warn("notify: no SENDGRID_API_KEY; build emails are off")
	return nil
}
