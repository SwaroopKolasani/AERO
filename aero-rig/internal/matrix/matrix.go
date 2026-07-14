package matrix

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aero-rig/internal/report"
	"aero-rig/internal/suite"
)

const SchemaV1 = "aerorig.matrix.v1"

type Matrix struct {
	SchemaVersion     string      `json:"schema_version"`
	SuiteName         string      `json:"suite_name"`
	SourceSuiteResult string      `json:"source_suite_result"`
	OutputDir         string      `json:"output_dir"`
	GeneratedAt       string      `json:"generated_at"`
	Rows              []MatrixRow `json:"rows"`
}

type MatrixRow struct {
	Name               string                 `json:"name"`
	Probe              string                 `json:"probe"`
	SummaryPath        string                 `json:"summary_path"`
	TotalSamples       int                    `json:"total_samples"`
	OKSamples          int                    `json:"ok_samples"`
	FailedSamples      int                    `json:"failed_samples"`
	SuccessRate        float64                `json:"success_rate"`
	LatencyMinMS       float64                `json:"latency_min_ms,omitempty"`
	LatencyP50MS       float64                `json:"latency_p50_ms,omitempty"`
	LatencyP95MS       float64                `json:"latency_p95_ms,omitempty"`
	LatencyMaxMS       float64                `json:"latency_max_ms,omitempty"`
	LatencyAvgMS       float64                `json:"latency_avg_ms,omitempty"`
	TTFTP50MS          float64                `json:"ttft_p50_ms,omitempty"`
	TTFTP95MS          float64                `json:"ttft_p95_ms,omitempty"`
	OutputTokensAvg    float64                `json:"output_tokens_avg,omitempty"`
	TotalTokensAvg     float64                `json:"total_tokens_avg,omitempty"`
	AnswerStable       *bool                  `json:"answer_stable,omitempty"`
	VerifiedSamples    int                    `json:"verified_samples,omitempty"`
	VerifiedHitSamples int                    `json:"verified_hit_samples,omitempty"`
	StatusCodes        []report.CountByStatus `json:"status_codes,omitempty"`
	CacheResults       []report.CountByString `json:"cache_results,omitempty"`
	Tiers              []report.CountByString `json:"tiers,omitempty"`
	Errors             []report.CountByString `json:"errors,omitempty"`
}

func BuildFromSuiteResult(path string) (Matrix, error) {
	var result suite.SuiteResult
	if err := readJSON(path, &result); err != nil {
		return Matrix{}, fmt.Errorf("read suite result: %w", err)
	}

	m := Matrix{
		SchemaVersion:     SchemaV1,
		SuiteName:         result.SuiteName,
		SourceSuiteResult: path,
		OutputDir:         result.OutputDir,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}

	for _, artifact := range result.HTTPArtifacts {
		if !isSummaryArtifact(artifact.Path, ".http.summary.json") {
			continue
		}

		var s report.HTTPSummary
		if err := readArtifactJSON(path, artifact.Path, &s); err != nil {
			return m, fmt.Errorf("read http summary %s: %w", artifact.Path, err)
		}

		m.Rows = append(m.Rows, MatrixRow{
			Name:          artifactBaseName(artifact.Name),
			Probe:         "http",
			SummaryPath:   artifact.Path,
			TotalSamples:  s.TotalSamples,
			OKSamples:     s.OKSamples,
			FailedSamples: s.FailedSamples,
			SuccessRate:   s.SuccessRate,
			LatencyMinMS:  s.LatencyMS.MinMS,
			LatencyP50MS:  s.LatencyMS.P50MS,
			LatencyP95MS:  s.LatencyMS.P95MS,
			LatencyMaxMS:  s.LatencyMS.MaxMS,
			LatencyAvgMS:  s.LatencyMS.AvgMS,
			StatusCodes:   s.StatusCodes,
			Errors:        s.Errors,
		})
	}

	for _, artifact := range result.ChatArtifacts {
		if !isSummaryArtifact(artifact.Path, ".chat.summary.json") {
			continue
		}

		var s report.ChatSummary
		if err := readArtifactJSON(path, artifact.Path, &s); err != nil {
			return m, fmt.Errorf("read chat summary %s: %w", artifact.Path, err)
		}

		answerStable := s.AnswerStable
		m.Rows = append(m.Rows, MatrixRow{
			Name:               artifactBaseName(artifact.Name),
			Probe:              "chat",
			SummaryPath:        artifact.Path,
			TotalSamples:       s.TotalSamples,
			OKSamples:          s.OKSamples,
			FailedSamples:      s.FailedSamples,
			SuccessRate:        s.SuccessRate,
			LatencyMinMS:       s.LatencyMS.MinMS,
			LatencyP50MS:       s.LatencyMS.P50MS,
			LatencyP95MS:       s.LatencyMS.P95MS,
			LatencyMaxMS:       s.LatencyMS.MaxMS,
			LatencyAvgMS:       s.LatencyMS.AvgMS,
			OutputTokensAvg:    s.OutputTokens.Avg,
			TotalTokensAvg:     s.TotalTokens.Avg,
			AnswerStable:       &answerStable,
			VerifiedSamples:    s.VerifiedSamples,
			VerifiedHitSamples: s.VerifiedHitSamples,
			StatusCodes:        s.StatusCodes,
			CacheResults:       s.CacheResults,
			Tiers:              s.Tiers,
			Errors:             s.Errors,
		})
	}

	for _, artifact := range result.StreamArtifacts {
		if !isSummaryArtifact(artifact.Path, ".chat_stream.summary.json") {
			continue
		}

		var s report.ChatStreamSummary
		if err := readArtifactJSON(path, artifact.Path, &s); err != nil {
			return m, fmt.Errorf("read chat stream summary %s: %w", artifact.Path, err)
		}

		answerStable := s.AnswerStable
		m.Rows = append(m.Rows, MatrixRow{
			Name:               artifactBaseName(artifact.Name),
			Probe:              "chat_stream",
			SummaryPath:        artifact.Path,
			TotalSamples:       s.TotalSamples,
			OKSamples:          s.OKSamples,
			FailedSamples:      s.FailedSamples,
			SuccessRate:        s.SuccessRate,
			LatencyMinMS:       s.DurationMS.MinMS,
			LatencyP50MS:       s.DurationMS.P50MS,
			LatencyP95MS:       s.DurationMS.P95MS,
			LatencyMaxMS:       s.DurationMS.MaxMS,
			LatencyAvgMS:       s.DurationMS.AvgMS,
			TTFTP50MS:          s.TTFTMS.P50MS,
			TTFTP95MS:          s.TTFTMS.P95MS,
			OutputTokensAvg:    s.OutputTokens.Avg,
			TotalTokensAvg:     s.TotalTokens.Avg,
			AnswerStable:       &answerStable,
			VerifiedSamples:    s.VerifiedSamples,
			VerifiedHitSamples: s.VerifiedHitSamples,
			StatusCodes:        s.StatusCodes,
			CacheResults:       s.CacheResults,
			Tiers:              s.Tiers,
			Errors:             s.Errors,
		})
	}

	return m, nil
}

func Write(path string, m Matrix) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create matrix: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("write matrix: %w", err)
	}

	return nil
}

func readArtifactJSON(suiteResultPath string, artifactPath string, v any) error {
	if err := readJSON(artifactPath, v); err == nil {
		return nil
	}

	if filepath.IsAbs(artifactPath) {
		return readJSON(artifactPath, v)
	}

	fallback := filepath.Join(filepath.Dir(suiteResultPath), filepath.Base(artifactPath))
	return readJSON(fallback, v)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return err
	}
	return nil
}

func isSummaryArtifact(path string, suffix string) bool {
	return strings.HasSuffix(path, suffix)
}

func artifactBaseName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, " summary")
	if name == "" {
		return "unnamed"
	}
	return name
}
