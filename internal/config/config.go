// Package config reads server settings from the environment.
package config

import (
	"errors"
	"os"
)

type Config struct {
	Port           string // PORT — Cloud Run sets this
	Env            string // APP_ENV — "dev" or "prod"
	ExperimentsDir string // EXPERIMENTS_DIR — holds <slug>/dist/ for each experiment
	SiteDir        string // SITE_DIR — the built wasm site bundle (site/dist)
	AdminUser      string // ADMIN_USER
	AdminPassword  string // ADMIN_PASSWORD — required in prod

	// front ends (shared by the main site and the user-content service)
	SigningKey        []byte // FRONTEND_SIGNING_KEY — HMAC key for fetoken; required in prod
	MainOrigin        string // MAIN_ORIGIN — the main site, e.g. https://benpriddy.com
	UsercontentOrigin string // USERCONTENT_ORIGIN — e.g. https://benpriddy-usercontent.com
	FrontendsDir      string // FRONTENDS_DIR — local front-end files: <dir>/<ref>/index.html
}

func Load() (Config, error) {
	c := Config{
		Port:           env("PORT", "8080"),
		Env:            env("APP_ENV", "dev"),
		ExperimentsDir: env("EXPERIMENTS_DIR", "experiments"),
		SiteDir:        env("SITE_DIR", "site/dist"),
		AdminUser:      env("ADMIN_USER", "admin"),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),

		SigningKey:        []byte(os.Getenv("FRONTEND_SIGNING_KEY")),
		MainOrigin:        env("MAIN_ORIGIN", "http://localhost:8090"),
		UsercontentOrigin: env("USERCONTENT_ORIGIN", "http://127.0.0.1:8091"),
		FrontendsDir:      env("FRONTENDS_DIR", "build/frontends"),
	}
	if len(c.SigningKey) == 0 {
		if c.Env == "prod" {
			return c, errors.New("FRONTEND_SIGNING_KEY is required when APP_ENV=prod")
		}
		// both dev processes must agree, so the dev key is fixed (and useless in prod)
		c.SigningKey = []byte("dev-insecure-frontend-signing-key")
	}
	if c.AdminPassword == "" {
		if c.Env == "prod" {
			return c, errors.New("ADMIN_PASSWORD is required when APP_ENV=prod")
		}
		c.AdminPassword = "dev"
	}
	return c, nil
}

func (c Config) Dev() bool { return c.Env != "prod" }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
