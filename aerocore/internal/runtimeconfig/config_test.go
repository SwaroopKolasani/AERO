package runtimeconfig

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(func(string) string {
		return ""
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":8088" {
		t.Fatalf("expected default addr, got %q", cfg.Addr)
	}

	if cfg.ReadTimeout != 5*time.Second {
		t.Fatalf("unexpected read timeout: %s", cfg.ReadTimeout)
	}

	if cfg.WriteTimeout != 30*time.Second {
		t.Fatalf("unexpected write timeout: %s", cfg.WriteTimeout)
	}

	if cfg.IdleTimeout != 120*time.Second {
		t.Fatalf("unexpected idle timeout: %s", cfg.IdleTimeout)
	}

	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected shutdown timeout: %s", cfg.ShutdownTimeout)
	}
}

func TestLoadUsesAeroCoreDefaultUpstreamFirst(t *testing.T) {
	env := map[string]string{
		"AEROCORE_DEFAULT_UPSTREAM_URL": "http://core-upstream.local:11434",
		"AERO_UPSTREAM_URL":             "http://legacy-upstream.local:11434",
	}

	cfg, err := Load(func(key string) string {
		return env[key]
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.DefaultUpstreamURL != "http://core-upstream.local:11434" {
		t.Fatalf("expected AEROCORE_DEFAULT_UPSTREAM_URL to win, got %q", cfg.DefaultUpstreamURL)
	}
}

func TestLoadFallsBackToAeroUpstreamURL(t *testing.T) {
	env := map[string]string{
		"AERO_UPSTREAM_URL": "http://legacy-upstream.local:11434",
	}

	cfg, err := Load(func(key string) string {
		return env[key]
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.DefaultUpstreamURL != "http://legacy-upstream.local:11434" {
		t.Fatalf("expected AERO_UPSTREAM_URL fallback, got %q", cfg.DefaultUpstreamURL)
	}
}

func TestLoadDurationOverrides(t *testing.T) {
	env := map[string]string{
		"AEROCORE_READ_TIMEOUT":     "2s",
		"AEROCORE_WRITE_TIMEOUT":    "3s",
		"AEROCORE_IDLE_TIMEOUT":     "4s",
		"AEROCORE_SHUTDOWN_TIMEOUT": "5s",
	}

	cfg, err := Load(func(key string) string {
		return env[key]
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.ReadTimeout != 2*time.Second {
		t.Fatalf("unexpected read timeout: %s", cfg.ReadTimeout)
	}

	if cfg.WriteTimeout != 3*time.Second {
		t.Fatalf("unexpected write timeout: %s", cfg.WriteTimeout)
	}

	if cfg.IdleTimeout != 4*time.Second {
		t.Fatalf("unexpected idle timeout: %s", cfg.IdleTimeout)
	}

	if cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("unexpected shutdown timeout: %s", cfg.ShutdownTimeout)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	env := map[string]string{
		"AEROCORE_READ_TIMEOUT": "bad",
	}

	_, err := Load(func(key string) string {
		return env[key]
	})

	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}

func TestLoadRejectsNonPositiveDuration(t *testing.T) {
	env := map[string]string{
		"AEROCORE_READ_TIMEOUT": "0s",
	}

	_, err := Load(func(key string) string {
		return env[key]
	})

	if err == nil {
		t.Fatal("expected non-positive duration error")
	}
}
