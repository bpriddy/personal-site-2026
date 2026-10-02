// Command usercontent runs the user-content service: it serves front-end files
// to the main site's sandboxed iframe at /t/<token>/<path>, and the host API at
// /site-host.js. It runs on a separate domain and holds only the signing key.
// See docs/frontend-protocol.md.
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
	"github.com/bpriddy/personal-site-2026/internal/media"
	"github.com/bpriddy/personal-site-2026/internal/usercontent"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load(config.Usercontent)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	// built-ins ship in the image (FRONTENDS_DIR); revisions of prompted front
	// ends come from FRONTENDS_BUCKET when set, else from FRONTENDS_DIR/rev/
	var src usercontent.FileSource = usercontent.NewDirSource(cfg.FrontendsDir)
	if cfg.FrontendsBucket != "" {
		gs, err := usercontent.NewGCSSource(context.Background(), cfg.FrontendsBucket, src)
		if err != nil {
			log.Error("frontends bucket", "err", err)
			os.Exit(1)
		}
		src = gs
		log.Info("revisions: cloud storage", "bucket", cfg.FrontendsBucket)
	}

	// media (/media/...): FRONTENDS_BUCKET objects media/..., else MEDIA_DIR
	mediaSrc, err := media.FromEnv(context.Background(), cfg.FrontendsBucket, cfg.MediaDir)
	if err != nil {
		log.Error("media", "err", err)
		os.Exit(1)
	}

	h, err := usercontent.New(usercontent.Options{
		SigningKey: cfg.SigningKey,
		MainOrigin: cfg.MainOrigin,
		Source:     src,
		Log:        log,
		Media:      media.New(mediaSrc, log),
	})
	if err != nil {
		log.Error("usercontent", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Cloud Run sends SIGTERM before stopping an instance
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", httpSrv.Addr, "env", cfg.Env,
			"frontends_dir", cfg.FrontendsDir, "main_origin", cfg.MainOrigin)
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
