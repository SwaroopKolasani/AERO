package workload

import (
	"os"
	"path/filepath"
	"testing"

	"aero-rig/internal/suite"
)

func TestBuildSuiteChatAndStream(t *testing.T) {
	m := Manifest{
		Name:      "local workload",
		OutputDir: "out/suites/local-workload",
		Targets: []TargetSpec{
			{
				Name:    "aerocache",
				Kind:    "both",
				URL:     "http://127.0.0.1:8080/v1/chat/completions",
				Model:   "tiny",
				Count:   2,
				Timeout: "10s",
			},
		},
		Cases: []CaseSpec{
			{
				Name:   "pong",
				Prompt: "Return exactly: pong",
				Proof: &suite.ProofSpec{
					RequireCacheHit:    true,
					RequireVerifiedHit: true,
					RequireMissHit:     true,
				},
			},
		},
	}

	s := BuildSuite(m)

	if s.Name != "local workload" {
		t.Fatalf("suite name = %q", s.Name)
	}
	if s.OutputDir != "out/suites/local-workload" {
		t.Fatalf("output dir = %q", s.OutputDir)
	}
	if len(s.Chat) != 1 {
		t.Fatalf("chat specs = %d, want 1", len(s.Chat))
	}
	if len(s.ChatStream) != 1 {
		t.Fatalf("stream specs = %d, want 1", len(s.ChatStream))
	}
	if s.Chat[0].Name != "aerocache-pong-chat" {
		t.Fatalf("chat name = %q", s.Chat[0].Name)
	}
	if s.ChatStream[0].Name != "aerocache-pong-stream" {
		t.Fatalf("stream name = %q", s.ChatStream[0].Name)
	}
	if s.Chat[0].Proof == nil {
		t.Fatal("expected proof on non-stream chat spec")
	}
}

func TestCaseOverridesTargetCountAndTimeout(t *testing.T) {
	m := Manifest{
		Name: "override workload",
		Targets: []TargetSpec{
			{
				Name:    "ollama",
				Kind:    "chat",
				URL:     "http://127.0.0.1:11434/v1/chat/completions",
				Model:   "tiny",
				Count:   2,
				Timeout: "10s",
			},
		},
		Cases: []CaseSpec{
			{
				Name:    "short",
				Prompt:  "pong",
				Count:   5,
				Timeout: "20s",
			},
		},
	}

	s := BuildSuite(m)

	if len(s.Chat) != 1 {
		t.Fatalf("chat specs = %d, want 1", len(s.Chat))
	}
	if s.Chat[0].Count != 5 {
		t.Fatalf("count = %d, want 5", s.Chat[0].Count)
	}
	if s.Chat[0].Timeout != "20s" {
		t.Fatalf("timeout = %q, want 20s", s.Chat[0].Timeout)
	}
}

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workload.json")

	raw := `{
	  "schema_version": "aerorig.workload.v1",
	  "name": "local",
	  "targets": [
	    {
	      "name": "ollama",
	      "kind": "chat",
	      "url": "http://127.0.0.1:11434/v1/chat/completions",
	      "model": "tiny"
	    }
	  ],
	  "cases": [
	    {
	      "name": "pong",
	      "prompt": "Return exactly: pong"
	    }
	  ]
	}`

	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "local" {
		t.Fatalf("name = %q", m.Name)
	}
	if len(m.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(m.Targets))
	}
}

func TestValidateRejectsBadManifest(t *testing.T) {
	err := Validate(Manifest{})
	if err == nil {
		t.Fatal("expected validation error")
	}

	err = Validate(Manifest{
		Name: "bad",
		Targets: []TargetSpec{
			{Name: "t", Kind: "invalid", URL: "http://127.0.0.1", Model: "tiny"},
		},
		Cases: []CaseSpec{
			{Name: "c", Prompt: "pong"},
		},
	})
	if err == nil {
		t.Fatal("expected invalid kind error")
	}
}

func TestWriteSuite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")

	err := WriteSuite(path, suite.Manifest{
		Name: "test",
		Chat: []suite.ChatProbeSpec{
			{Name: "chat", Target: "http://127.0.0.1", Model: "tiny", Prompt: "pong"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("expected non-empty suite file")
	}
}
