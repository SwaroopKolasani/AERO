package report

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSummarizeChatStream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	data := `
{"schema_version":"aerorig.chat_stream_probe.v1","probe":"chat_stream","sample":1,"target_name":"aerocache","model":"tiny","ttft_ms":20.0,"duration_ms":50.0,"status_code":200,"ok":true,"response_model":"tiny","finish_reason":"stop","answer_sha256":"abc","answer_bytes":4,"sse_events":4,"content_chunks":2,"prompt_tokens":5,"output_tokens":1,"total_tokens":6,"aero_headers":{"X-Aero-Cache":"miss","X-Aero-Tier":"A","X-Aero-Verified":"false"}}
{"schema_version":"aerorig.chat_stream_probe.v1","probe":"chat_stream","sample":2,"target_name":"aerocache","model":"tiny","ttft_ms":5.0,"duration_ms":10.0,"status_code":200,"ok":true,"response_model":"tiny","finish_reason":"stop","answer_sha256":"abc","answer_bytes":4,"sse_events":4,"content_chunks":2,"prompt_tokens":5,"output_tokens":1,"total_tokens":6,"aero_headers":{"X-Aero-Cache":"hit","X-Aero-Tier":"A","X-Aero-Verified":"true"}}
{"schema_version":"aerorig.chat_stream_probe.v1","probe":"chat_stream","sample":3,"target_name":"bad","model":"tiny","duration_ms":1.0,"ok":false,"error":"connection refused"}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SummarizeChatStream([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if s.SchemaVersion != ChatStreamSummarySchemaV1 {
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
	if s.TTFTMS.MinMS != 5.0 || s.TTFTMS.P50MS != 5.0 || s.TTFTMS.P95MS != 20.0 {
		t.Fatalf("unexpected ttft summary: %+v", s.TTFTMS)
	}
	if s.DurationMS.MinMS != 1.0 || s.DurationMS.P50MS != 10.0 || s.DurationMS.P95MS != 50.0 {
		t.Fatalf("unexpected duration summary: %+v", s.DurationMS)
	}
	if s.ContentChunks.Min != 2 || s.ContentChunks.Max != 2 {
		t.Fatalf("unexpected chunk summary: %+v", s.ContentChunks)
	}
	if s.AnswerHashCount != 1 {
		t.Fatalf("answer_hash_count = %d, want 1", s.AnswerHashCount)
	}
	if !s.AnswerStable {
		t.Fatal("expected answer_stable=true")
	}
	if s.VerifiedSamples != 1 {
		t.Fatalf("verified_samples = %d, want 1", s.VerifiedSamples)
	}
	if s.VerifiedHitSamples != 1 {
		t.Fatalf("verified_hit_samples = %d, want 1", s.VerifiedHitSamples)
	}
	if len(s.CacheResults) != 2 {
		t.Fatalf("unexpected cache counts: %+v", s.CacheResults)
	}
	if len(s.Errors) != 1 || s.Errors[0].Count != 1 {
		t.Fatalf("unexpected errors: %+v", s.Errors)
	}
}

func TestSummarizeChatStreamDetectsAnswerInstability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unstable.jsonl")

	data := `
{"schema_version":"aerorig.chat_stream_probe.v1","probe":"chat_stream","sample":1,"ttft_ms":5.0,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"abc"}
{"schema_version":"aerorig.chat_stream_probe.v1","probe":"chat_stream","sample":2,"ttft_ms":5.0,"duration_ms":10.0,"status_code":200,"ok":true,"answer_sha256":"def"}
`

	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SummarizeChatStream([]string{path})
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

func TestSummarizeChatStreamRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")

	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := SummarizeChatStream([]string{path})
	if err == nil {
		t.Fatal("expected error")
	}
}
