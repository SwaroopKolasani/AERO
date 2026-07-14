package report

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSummarizeChat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	data := `
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":1,"target_name":"aerocache","target_url":"http://127.0.0.1:8080/v1/chat/completions","model":"tiny","duration_ms":30.0,"status_code":200,"ok":true,"response_model":"tiny","finish_reason":"stop","answer_sha256":"abc","answer_bytes":4,"prompt_tokens":5,"output_tokens":1,"total_tokens":6,"aero_headers":{"X-Aero-Cache":"miss","X-Aero-Tier":"A","X-Aero-Verified":"false"}}
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":2,"target_name":"aerocache","target_url":"http://127.0.0.1:8080/v1/chat/completions","model":"tiny","duration_ms":10.0,"status_code":200,"ok":true,"response_model":"tiny","finish_reason":"stop","answer_sha256":"abc","answer_bytes":4,"prompt_tokens":5,"output_tokens":1,"total_tokens":6,"aero_headers":{"X-Aero-Cache":"hit","X-Aero-Tier":"A","X-Aero-Verified":"true"}}
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":3,"target_name":"bad","target_url":"http://127.0.0.1:1/v1/chat/completions","model":"tiny","duration_ms":1.0,"ok":false,"error":"connection refused"}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SummarizeChat([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if s.SchemaVersion != ChatSummarySchemaV1 {
		t.Fatalf("schema = %q", s.SchemaVersion)
	}
	if s.TotalSamples != 3 {
		t.Fatalf("total = %d, want 3", s.TotalSamples)
	}
	if s.OKSamples != 2 {
		t.Fatalf("ok = %d, want 2", s.OKSamples)
	}
	if s.FailedSamples != 1 {
		t.Fatalf("failed = %d, want 1", s.FailedSamples)
	}
	if s.AnswerHashCount != 1 {
		t.Fatalf("answer_hash_count = %d, want 1", s.AnswerHashCount)
	}
	if !s.AnswerStable {
		t.Fatal("expected answer_stable=true")
	}
	if s.VerifiedSamples != 1 {
		t.Fatalf("verified = %d, want 1", s.VerifiedSamples)
	}
	if s.LatencyMS.MinMS != 1.0 || s.LatencyMS.P50MS != 10.0 || s.LatencyMS.P95MS != 30.0 {
		t.Fatalf("unexpected latency summary: %+v", s.LatencyMS)
	}
	if s.TotalTokens.Min != 6 || s.TotalTokens.Max != 6 || s.TotalTokens.Avg != 6.0 {
		t.Fatalf("unexpected total token summary: %+v", s.TotalTokens)
	}
	if len(s.CacheResults) != 2 {
		t.Fatalf("unexpected cache counts: %+v", s.CacheResults)
	}
	if len(s.Errors) != 1 || s.Errors[0].Count != 1 {
		t.Fatalf("unexpected errors: %+v", s.Errors)
	}
}

func TestSummarizeChatDetectsAnswerInstability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unstable.jsonl")

	data := `
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":1,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"abc"}
{"schema_version":"aerorig.chat_probe.v1","probe":"chat","sample":2,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"def"}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SummarizeChat([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if s.AnswerStable {
		t.Fatal("expected answer_stable=false")
	}
	if s.AnswerHashCount != 2 {
		t.Fatalf("answer_hash_count = %d, want 2", s.AnswerHashCount)
	}
}

func TestSummarizeChatRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")

	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := SummarizeChat([]string{path})
	if err == nil {
		t.Fatal("expected error")
	}
}
