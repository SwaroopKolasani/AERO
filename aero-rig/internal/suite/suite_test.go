package suite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunSuiteHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()

	res, err := Run(context.Background(), Manifest{
		Name:      "test suite",
		OutputDir: dir,
		HTTP: []HTTPProbeSpec{
			{
				Name:    "health",
				Target:  srv.URL,
				Count:   2,
				Timeout: "1s",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed {
		t.Fatalf("expected suite to pass: %+v", res.Errors)
	}
	if len(res.HTTPArtifacts) != 2 {
		t.Fatalf("http artifacts = %d, want 2", len(res.HTTPArtifacts))
	}

	assertFileExists(t, filepath.Join(dir, "health.http.jsonl"))
	assertFileExists(t, filepath.Join(dir, "health.http.summary.json"))
	assertFileExists(t, filepath.Join(dir, "suite_result.json"))
}

func TestRunSuiteChatWithProof(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("X-Aero-Cache", "miss")
			w.Header().Set("X-Aero-Verified", "false")
		} else {
			w.Header().Set("X-Aero-Cache", "hit")
			w.Header().Set("X-Aero-Verified", "true")
		}
		w.Header().Set("X-Aero-Tier", "A")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"model":"tiny",
			"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"pong"}}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
		}`))
	}))
	defer srv.Close()

	dir := t.TempDir()

	res, err := Run(context.Background(), Manifest{
		Name:      "chat suite",
		OutputDir: dir,
		Chat: []ChatProbeSpec{
			{
				Name:    "cache proof",
				Target:  srv.URL,
				Model:   "tiny",
				Prompt:  "ping",
				Count:   2,
				Timeout: "1s",
				Proof: &ProofSpec{
					RequireCacheHit:    true,
					RequireVerifiedHit: true,
					RequireMissHit:     true,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed {
		t.Fatalf("expected suite to pass: %+v", res.Errors)
	}

	assertFileExists(t, filepath.Join(dir, "cache-proof.chat.jsonl"))
	assertFileExists(t, filepath.Join(dir, "cache-proof.chat.summary.json"))
	assertFileExists(t, filepath.Join(dir, "cache-proof.chat.proof.json"))
	assertFileExists(t, filepath.Join(dir, "suite_result.json"))
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")

	raw := `{"name":"local","http":[{"name":"health","target":"http://127.0.0.1:8080/healthz"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "local" {
		t.Fatalf("name=%q", m.Name)
	}
	if len(m.HTTP) != 1 {
		t.Fatalf("http probes=%d", len(m.HTTP))
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected manifest to marshal")
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected file %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected non-empty file %s", path)
	}
}
