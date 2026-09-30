// Package config reads server settings from the environment.
package config

import (
	"errors"
	"os"
)

type Config struct {
	Port          string // PORT — Cloud Run sets this
	Env           string // APP_ENV — "dev" or "prod"
	AdminUser     string // ADMIN_USER
	AdminPassword string // ADMIN_PASSWORD — required in prod
	DatabaseURL   string // DATABASE_URL — Postgres; empty in dev means the in-memory store

	// AI (builder + observer). Empty disables both features, with a warning.
	AnthropicAPIKey string // ANTHROPIC_API_KEY — from secret anthropic-api-key-never in prod

	// front ends (shared by the main site and the user-content service)
	SigningKey        []byte // FRONTEND_SIGNING_KEY — HMAC key for fetoken; required in prod
	MainOrigin        string // MAIN_ORIGIN — the main site, e.g. https://benpriddy.com
	UsercontentOrigin string // USERCONTENT_ORIGIN — e.g. https://benpriddy-usercontent.com
	FrontendsDir      string // FRONTENDS_DIR — local front-end files: <dir>/<ref>/index.html
	FrontendsBucket   string // FRONTENDS_BUCKET — GCS bucket for revision files; empty = FRONTENDS_DIR
}

// Role says which binary is loading config; each requires only its own secrets
// in prod (the user-content service must never hold admin or database creds).
type Role int

const (
	Main        Role = iota // cmd/server
	Usercontent             // cmd/usercontent
)

func Load(role Role) (Config, error) {
	c := Config{
		Port:          env("PORT", "8080"),
		Env:           env("APP_ENV", "dev"),
		AdminUser:     env("ADMIN_USER", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),

		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),

		SigningKey:        []byte(os.Getenv("FRONTEND_SIGNING_KEY")),
		MainOrigin:        env("MAIN_ORIGIN", "http://localhost:8090"),
		UsercontentOrigin: env("USERCONTENT_ORIGIN", "http://127.0.0.1:8091"),
		FrontendsDir:      env("FRONTENDS_DIR", "build/frontends"),
		FrontendsBucket:   os.Getenv("FRONTENDS_BUCKET"),
	}
	if len(c.SigningKey) == 0 {
		if c.Env == "prod" {
			return c, errors.New("FRONTEND_SIGNING_KEY is required when APP_ENV=prod")
		}
		// both dev processes must agree, so the dev key is fixed (and useless in prod)
		c.SigningKey = []byte("dev-insecure-frontend-signing-key")
	}
	if role != Main {
		return c, nil
	}
	if c.DatabaseURL == "" && c.Env == "prod" {
		return c, errors.New("DATABASE_URL is required when APP_ENV=prod")
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
