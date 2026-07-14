package matrix

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const CompareSchemaV1 = "aerorig.matrix_compare.v1"

type Comparison struct {
	SchemaVersion string            `json:"schema_version"`
	BaselinePath  string            `json:"baseline_path"`
	CandidatePath string            `json:"candidate_path"`
	GeneratedAt   string            `json:"generated_at"`
	Summary       ComparisonSummary `json:"summary"`
	Rows          []ComparisonRow   `json:"rows"`
}

type ComparisonSummary struct {
	MatchedRows int `json:"matched_rows"`
	AddedRows   int `json:"added_rows"`
	RemovedRows int `json:"removed_rows"`
	FailedRows  int `json:"failed_rows"`
}

type ComparisonRow struct {
	Name                    string     `json:"name"`
	Probe                   string     `json:"probe"`
	Status                  string     `json:"status"`
	Baseline                *MatrixRow `json:"baseline,omitempty"`
	Candidate               *MatrixRow `json:"candidate,omitempty"`
	SuccessRateDelta        float64    `json:"success_rate_delta,omitempty"`
	LatencyP50DeltaMS       float64    `json:"latency_p50_delta_ms,omitempty"`
	LatencyP95DeltaMS       float64    `json:"latency_p95_delta_ms,omitempty"`
	LatencyAvgDeltaMS       float64    `json:"latency_avg_delta_ms,omitempty"`
	TTFTP50DeltaMS          float64    `json:"ttft_p50_delta_ms,omitempty"`
	TTFTP95DeltaMS          float64    `json:"ttft_p95_delta_ms,omitempty"`
	OutputTokensAvgDelta    float64    `json:"output_tokens_avg_delta,omitempty"`
	TotalTokensAvgDelta     float64    `json:"total_tokens_avg_delta,omitempty"`
	VerifiedSamplesDelta    int        `json:"verified_samples_delta,omitempty"`
	VerifiedHitSamplesDelta int        `json:"verified_hit_samples_delta,omitempty"`
	AnswerStableChanged     bool       `json:"answer_stable_changed,omitempty"`
	BaselineAnswerStable    *bool      `json:"baseline_answer_stable,omitempty"`
	CandidateAnswerStable   *bool      `json:"candidate_answer_stable,omitempty"`
}

func CompareFiles(baselinePath string, candidatePath string) (Comparison, error) {
	var baseline Matrix
	if err := readJSON(baselinePath, &baseline); err != nil {
		return Comparison{}, fmt.Errorf("read baseline matrix: %w", err)
	}

	var candidate Matrix
	if err := readJSON(candidatePath, &candidate); err != nil {
		return Comparison{}, fmt.Errorf("read candidate matrix: %w", err)
	}

	return Compare(baselinePath, candidatePath, baseline, candidate), nil
}

func Compare(baselinePath string, candidatePath string, baseline Matrix, candidate Matrix) Comparison {
	out := Comparison{
		SchemaVersion: CompareSchemaV1,
		BaselinePath:  baselinePath,
		CandidatePath: candidatePath,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}

	baseRows := map[string]MatrixRow{}
	candRows := map[string]MatrixRow{}
	keys := map[string]bool{}

	for _, row := range baseline.Rows {
		key := rowKey(row)
		baseRows[key] = row
		keys[key] = true
	}

	for _, row := range candidate.Rows {
		key := rowKey(row)
		candRows[key] = row
		keys[key] = true
	}

	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	for _, key := range orderedKeys {
		base, hasBase := baseRows[key]
		cand, hasCand := candRows[key]

		switch {
		case hasBase && hasCand:
			row := compareMatchedRow(base, cand)
			out.Rows = append(out.Rows, row)
			out.Summary.MatchedRows++
			if cand.FailedSamples > 0 {
				out.Summary.FailedRows++
			}
		case hasBase && !hasCand:
			baseCopy := base
			out.Rows = append(out.Rows, ComparisonRow{
				Name:     base.Name,
				Probe:    base.Probe,
				Status:   "removed",
				Baseline: &baseCopy,
			})
			out.Summary.RemovedRows++
		case !hasBase && hasCand:
			candCopy := cand
			out.Rows = append(out.Rows, ComparisonRow{
				Name:      cand.Name,
				Probe:     cand.Probe,
				Status:    "added",
				Candidate: &candCopy,
			})
			out.Summary.AddedRows++
			if cand.FailedSamples > 0 {
				out.Summary.FailedRows++
			}
		}
	}

	return out
}

func compareMatchedRow(base MatrixRow, cand MatrixRow) ComparisonRow {
	baseCopy := base
	candCopy := cand

	row := ComparisonRow{
		Name:                    cand.Name,
		Probe:                   cand.Probe,
		Status:                  "matched",
		Baseline:                &baseCopy,
		Candidate:               &candCopy,
		SuccessRateDelta:        round3(cand.SuccessRate - base.SuccessRate),
		LatencyP50DeltaMS:       round3(cand.LatencyP50MS - base.LatencyP50MS),
		LatencyP95DeltaMS:       round3(cand.LatencyP95MS - base.LatencyP95MS),
		LatencyAvgDeltaMS:       round3(cand.LatencyAvgMS - base.LatencyAvgMS),
		TTFTP50DeltaMS:          round3(cand.TTFTP50MS - base.TTFTP50MS),
		TTFTP95DeltaMS:          round3(cand.TTFTP95MS - base.TTFTP95MS),
		OutputTokensAvgDelta:    round3(cand.OutputTokensAvg - base.OutputTokensAvg),
		TotalTokensAvgDelta:     round3(cand.TotalTokensAvg - base.TotalTokensAvg),
		VerifiedSamplesDelta:    cand.VerifiedSamples - base.VerifiedSamples,
		VerifiedHitSamplesDelta: cand.VerifiedHitSamples - base.VerifiedHitSamples,
		BaselineAnswerStable:    base.AnswerStable,
		CandidateAnswerStable:   cand.AnswerStable,
	}

	if base.AnswerStable != nil && cand.AnswerStable != nil {
		row.AnswerStableChanged = *base.AnswerStable != *cand.AnswerStable
	}

	return row
}

func rowKey(row MatrixRow) string {
	return strings.ToLower(strings.TrimSpace(row.Probe)) + "\x00" + strings.ToLower(strings.TrimSpace(row.Name))
}
