package config

import "testing"

func TestProdRequirementsByRole(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("FRONTEND_SIGNING_KEY", "k")
	// the user-content service needs only the signing key
	if _, err := Load(Usercontent); err != nil {
		t.Fatalf("usercontent without admin/db creds: %v", err)
	}
	if _, err := Load(Main); err == nil {
		t.Fatal("main without DATABASE_URL/ADMIN_PASSWORD should fail in prod")
	}
	t.Setenv("DATABASE_URL", "postgres://x")
	if _, err := Load(Main); err == nil {
		t.Fatal("main without ADMIN_PASSWORD should fail in prod")
	}
	t.Setenv("ADMIN_PASSWORD", "p")
	if _, err := Load(Main); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRONTEND_SIGNING_KEY", "")
	if _, err := Load(Usercontent); err == nil {
		t.Fatal("usercontent without a signing key should fail in prod")
	}
}

func TestDevDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	c, err := Load(Main)
	if err != nil || c.DatabaseURL != "" || c.AdminPassword != "dev" || len(c.SigningKey) == 0 {
		t.Fatalf("%+v, %v", c, err)
	}
}
