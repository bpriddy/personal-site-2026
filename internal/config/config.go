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
	AdminUser      string // ADMIN_USER
	AdminPassword  string // ADMIN_PASSWORD — required in prod
}

func Load() (Config, error) {
	c := Config{
		Port:           env("PORT", "8080"),
		Env:            env("APP_ENV", "dev"),
		ExperimentsDir: env("EXPERIMENTS_DIR", "experiments"),
		AdminUser:      env("ADMIN_USER", "admin"),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
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
