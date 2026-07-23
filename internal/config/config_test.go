package config

import "testing"

func TestLoadBuildsDatabaseURLFromFugueBinding(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres.internal")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("DB_NAME", "sleep data")
	t.Setenv("DB_USER", "sleep user")
	t.Setenv("DB_PASSWORD", "p@ss/word")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := "postgres://sleep%20user:p%40ss%2Fword@postgres.internal:5433/sleep%20data?sslmode=disable"
	if cfg.DatabaseURL != want {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestLoadPrefersExplicitDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://explicit.example/app")
	t.Setenv("DB_HOST", "postgres.internal")
	t.Setenv("DB_NAME", "baomian")
	t.Setenv("DB_USER", "baomian")
	t.Setenv("DB_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseURL != "postgres://explicit.example/app" {
		t.Fatalf("DatabaseURL = %q, want explicit DATABASE_URL", cfg.DatabaseURL)
	}
}
