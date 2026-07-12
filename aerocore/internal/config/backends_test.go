package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

func TestLoadBackendsWrappedObject(t *testing.T) {
	path := writeTempFile(t, `{
		"backends": [
			{
				"id": "mac-m2-ollama",
				"rung": "fleet",
				"url": "http://mac.local:11434",
				"healthy": true,
				"loaded_models": ["llama3.2:3b"],
				"capable_models": ["llama3.2:3b"],
				"p95_latency_ms": 900,
				"max_context": 8192
			}
		]
	}`)

	got, err := LoadBackends(path)
	if err != nil {
		t.Fatalf("LoadBackends returned error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(got))
	}

	if got[0].ID != "mac-m2-ollama" {
		t.Fatalf("expected mac backend, got %+v", got[0])
	}

	if got[0].Rung != placement.RungFleet {
		t.Fatalf("expected fleet rung, got %+v", got[0])
	}
}

func TestLoadBackendsArray(t *testing.T) {
	path := writeTempFile(t, `[
		{
			"id": "cloud-gate",
			"rung": "gate",
			"url": "https://cloud.example/v1",
			"healthy": true,
			"capable_models": ["llama3.2:70b"],
			"cost_per_1k_tokens": 0.01,
			"p95_latency_ms": 1200,
			"max_context": 32768
		}
	]`)

	got, err := LoadBackends(path)
	if err != nil {
		t.Fatalf("LoadBackends returned error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(got))
	}

	if got[0].ID != "cloud-gate" {
		t.Fatalf("expected cloud backend, got %+v", got[0])
	}
}

func TestLoadBackendsRejectsMissingID(t *testing.T) {
	path := writeTempFile(t, `{
		"backends": [
			{
				"rung": "fleet",
				"url": "http://mac.local:11434",
				"healthy": true
			}
		]
	}`)

	_, err := LoadBackends(path)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadBackendsRejectsDuplicateID(t *testing.T) {
	path := writeTempFile(t, `{
		"backends": [
			{
				"id": "mac-m2-ollama",
				"rung": "fleet",
				"url": "http://mac.local:11434"
			},
			{
				"id": "mac-m2-ollama",
				"rung": "gate",
				"url": "https://cloud.example/v1"
			}
		]
	}`)

	_, err := LoadBackends(path)
	if err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func writeTempFile(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "backends.json")

	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	return path
}
