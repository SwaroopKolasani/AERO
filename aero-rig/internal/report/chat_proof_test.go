package report

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildChatProofPassesForMissThenVerifiedHit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	data := `
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":1,"duration_ms":30.0,"status_code":200,"ok":true,"answer_sha256":"abc","aero_headers":{"X-Aero-Cache":"miss","X-Aero-Tier":"A","X-Aero-Verified":"false"}}
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":2,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"abc","aero_headers":{"X-Aero-Cache":"hit","X-Aero-Tier":"A","X-Aero-Verified":"true"}}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := BuildChatProof([]string{path}, ChatProofOptions{
		RequireCacheHit:    true,
		RequireVerifiedHit: true,
		RequireMissThenHit: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !p.Passed {
		t.Fatalf("expected proof to pass: %+v", p.Assertions)
	}
	if !p.AnswerStable {
		t.Fatal("expected stable answer")
	}
	if p.CacheMissSamples != 1 || p.CacheHitSamples != 1 {
		t.Fatalf("unexpected cache counts: miss=%d hit=%d", p.CacheMissSamples, p.CacheHitSamples)
	}
	if p.VerifiedHitSamples != 1 {
		t.Fatalf("verified_hit_samples=%d, want 1", p.VerifiedHitSamples)
	}
	if !p.MissThenHit {
		t.Fatal("expected miss_then_hit")
	}
}

func TestBuildChatProofFailsForDifferentAnswers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	data := `
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":1,"duration_ms":30.0,"status_code":200,"ok":true,"answer_sha256":"abc"}
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":2,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"def"}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := BuildChatProof([]string{path}, ChatProofOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if p.Passed {
		t.Fatalf("expected proof to fail: %+v", p.Assertions)
	}
	if p.AnswerStable {
		t.Fatal("expected unstable answer")
	}
}

func TestBuildChatProofFailsForMissingRequiredHit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	data := `
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":1,"duration_ms":30.0,"status_code":200,"ok":true,"answer_sha256":"abc","aero_headers":{"X-Aero-Cache":"miss"}}
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":2,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"abc","aero_headers":{"X-Aero-Cache":"miss"}}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := BuildChatProof([]string{path}, ChatProofOptions{RequireCacheHit: true})
	if err != nil {
		t.Fatal(err)
	}

	if p.Passed {
		t.Fatalf("expected proof to fail: %+v", p.Assertions)
	}
}
