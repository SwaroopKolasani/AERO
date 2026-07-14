package matrix

import (
	"path/filepath"
	"testing"
)

func TestCompareMatchedRows(t *testing.T) {
	stable := true

	base := Matrix{
		SchemaVersion: SchemaV1,
		SuiteName:     "baseline",
		Rows: []MatrixRow{
			{
				Name:               "stream",
				Probe:              "chat_stream",
				TotalSamples:       2,
				OKSamples:          2,
				SuccessRate:        1.0,
				LatencyP50MS:       100,
				LatencyP95MS:       150,
				LatencyAvgMS:       110,
				TTFTP50MS:          40,
				TTFTP95MS:          60,
				OutputTokensAvg:    5,
				TotalTokensAvg:     10,
				AnswerStable:       &stable,
				VerifiedSamples:    1,
				VerifiedHitSamples: 1,
			},
		},
	}

	cand := Matrix{
		SchemaVersion: SchemaV1,
		SuiteName:     "candidate",
		Rows: []MatrixRow{
			{
				Name:               "stream",
				Probe:              "chat_stream",
				TotalSamples:       2,
				OKSamples:          2,
				SuccessRate:        1.0,
				LatencyP50MS:       70,
				LatencyP95MS:       120,
				LatencyAvgMS:       80,
				TTFTP50MS:          20,
				TTFTP95MS:          30,
				OutputTokensAvg:    5,
				TotalTokensAvg:     10,
				AnswerStable:       &stable,
				VerifiedSamples:    2,
				VerifiedHitSamples: 2,
			},
		},
	}

	comp := Compare("base.json", "cand.json", base, cand)

	if comp.SchemaVersion != CompareSchemaV1 {
		t.Fatalf("schema = %q", comp.SchemaVersion)
	}
	if comp.Summary.MatchedRows != 1 {
		t.Fatalf("matched = %d, want 1", comp.Summary.MatchedRows)
	}
	if len(comp.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(comp.Rows))
	}

	row := comp.Rows[0]
	if row.Status != "matched" {
		t.Fatalf("status = %q", row.Status)
	}
	if row.LatencyP50DeltaMS != -30 {
		t.Fatalf("p50 delta = %v, want -30", row.LatencyP50DeltaMS)
	}
	if row.TTFTP50DeltaMS != -20 {
		t.Fatalf("ttft delta = %v, want -20", row.TTFTP50DeltaMS)
	}
	if row.VerifiedHitSamplesDelta != 1 {
		t.Fatalf("verified hit delta = %d, want 1", row.VerifiedHitSamplesDelta)
	}
	if row.AnswerStableChanged {
		t.Fatal("did not expect answer_stable change")
	}
}

func TestCompareAddedAndRemovedRows(t *testing.T) {
	base := Matrix{
		SchemaVersion: SchemaV1,
		Rows: []MatrixRow{
			{Name: "old", Probe: "http", SuccessRate: 1},
		},
	}

	cand := Matrix{
		SchemaVersion: SchemaV1,
		Rows: []MatrixRow{
			{Name: "new", Probe: "http", SuccessRate: 1},
		},
	}

	comp := Compare("base.json", "cand.json", base, cand)

	if comp.Summary.AddedRows != 1 {
		t.Fatalf("added = %d, want 1", comp.Summary.AddedRows)
	}
	if comp.Summary.RemovedRows != 1 {
		t.Fatalf("removed = %d, want 1", comp.Summary.RemovedRows)
	}
	if len(comp.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(comp.Rows))
	}
}

func TestCompareFiles(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	candPath := filepath.Join(dir, "cand.json")

	if err := Write(basePath, Matrix{
		SchemaVersion: SchemaV1,
		SuiteName:     "base",
		Rows: []MatrixRow{
			{Name: "health", Probe: "http", TotalSamples: 1, OKSamples: 1, SuccessRate: 1, LatencyP50MS: 10},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := Write(candPath, Matrix{
		SchemaVersion: SchemaV1,
		SuiteName:     "cand",
		Rows: []MatrixRow{
			{Name: "health", Probe: "http", TotalSamples: 1, OKSamples: 1, SuccessRate: 1, LatencyP50MS: 8},
		},
	}); err != nil {
		t.Fatal(err)
	}

	comp, err := CompareFiles(basePath, candPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(comp.Rows))
	}
	if comp.Rows[0].LatencyP50DeltaMS != -2 {
		t.Fatalf("delta = %v, want -2", comp.Rows[0].LatencyP50DeltaMS)
	}
}
