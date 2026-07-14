package matrix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aero-rig/internal/report"
	"aero-rig/internal/suite"
)

func TestBuildFromSuiteResult(t *testing.T) {
	dir := t.TempDir()

	httpSummaryPath := filepath.Join(dir, "health.http.summary.json")
	chatSummaryPath := filepath.Join(dir, "chat.chat.summary.json")
	streamSummaryPath := filepath.Join(dir, "stream.chat_stream.summary.json")
	suiteResultPath := filepath.Join(dir, "suite_result.json")

	writeJSONForTest(t, httpSummaryPath, report.HTTPSummary{
		SchemaVersion: report.HTTPSummarySchemaV1,
		Probe:         "http_summary",
		TotalSamples:  2,
		OKSamples:     2,
		SuccessRate:   1,
		LatencyMS: report.LatencySummary{
			MinMS: 1,
			P50MS: 2,
			P95MS: 3,
			MaxMS: 4,
			AvgMS: 2.5,
		},
		StatusCodes: []report.CountByStatus{{StatusCode: 200, Count: 2}},
	})

	writeJSONForTest(t, chatSummaryPath, report.ChatSummary{
		SchemaVersion:      report.ChatSummarySchemaV1,
		Probe:              "chat_summary",
		TotalSamples:       2,
		OKSamples:          2,
		SuccessRate:        1,
		AnswerStable:       true,
		VerifiedSamples:    1,
		VerifiedHitSamples: 1,
		LatencyMS: report.LatencySummary{
			MinMS: 10,
			P50MS: 20,
			P95MS: 30,
			MaxMS: 40,
			AvgMS: 25,
		},
		OutputTokens: report.IntSummary{Avg: 3},
		TotalTokens:  report.IntSummary{Avg: 8},
		CacheResults: []report.CountByString{{Value: "hit", Count: 1}, {Value: "miss", Count: 1}},
	})

	writeJSONForTest(t, streamSummaryPath, report.ChatStreamSummary{
		SchemaVersion:      report.ChatStreamSummarySchemaV1,
		Probe:              "chat_stream_summary",
		TotalSamples:       2,
		OKSamples:          2,
		SuccessRate:        1,
		AnswerStable:       true,
		VerifiedSamples:    1,
		VerifiedHitSamples: 1,
		DurationMS: report.LatencySummary{
			MinMS: 11,
			P50MS: 21,
			P95MS: 31,
			MaxMS: 41,
			AvgMS: 26,
		},
		TTFTMS:       report.LatencySummary{P50MS: 5, P95MS: 9},
		OutputTokens: report.IntSummary{Avg: 3},
		TotalTokens:  report.IntSummary{Avg: 8},
		CacheResults: []report.CountByString{{Value: "hit", Count: 1}, {Value: "miss", Count: 1}},
	})

	writeJSONForTest(t, suiteResultPath, suite.SuiteResult{
		SchemaVersion: suite.ResultSchemaV1,
		SuiteName:     "test-suite",
		OutputDir:     dir,
		Passed:        true,
		HTTPArtifacts: []suite.Artifact{
			{Name: "health raw", Path: filepath.Join(dir, "health.http.jsonl")},
			{Name: "health summary", Path: httpSummaryPath},
		},
		ChatArtifacts: []suite.Artifact{
			{Name: "chat raw", Path: filepath.Join(dir, "chat.chat.jsonl")},
			{Name: "chat summary", Path: chatSummaryPath},
		},
		StreamArtifacts: []suite.Artifact{
			{Name: "stream raw", Path: filepath.Join(dir, "stream.chat_stream.jsonl")},
			{Name: "stream summary", Path: streamSummaryPath},
		},
	})

	m, err := BuildFromSuiteResult(suiteResultPath)
	if err != nil {
		t.Fatal(err)
	}

	if m.SchemaVersion != SchemaV1 {
		t.Fatalf("schema = %q", m.SchemaVersion)
	}
	if m.SuiteName != "test-suite" {
		t.Fatalf("suite = %q", m.SuiteName)
	}
	if len(m.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(m.Rows))
	}

	if m.Rows[0].Probe != "http" {
		t.Fatalf("row 0 probe = %q", m.Rows[0].Probe)
	}
	if m.Rows[1].Probe != "chat" {
		t.Fatalf("row 1 probe = %q", m.Rows[1].Probe)
	}
	if m.Rows[2].Probe != "chat_stream" {
		t.Fatalf("row 2 probe = %q", m.Rows[2].Probe)
	}
	if m.Rows[2].TTFTP50MS != 5 {
		t.Fatalf("stream ttft p50 = %v, want 5", m.Rows[2].TTFTP50MS)
	}
	if m.Rows[1].AnswerStable == nil || !*m.Rows[1].AnswerStable {
		t.Fatal("expected chat answer_stable=true")
	}
}

func TestWriteMatrix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "matrix.json")

	err := Write(path, Matrix{
		SchemaVersion: SchemaV1,
		SuiteName:     "test",
		Rows:          []MatrixRow{{Name: "health", Probe: "http"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("expected non-empty matrix file")
	}
}

func writeJSONForTest(t *testing.T, path string, v any) {
	t.Helper()

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
