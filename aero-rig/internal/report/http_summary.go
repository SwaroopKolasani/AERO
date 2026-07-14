package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"aero-rig/internal/probe"
)

const HTTPSummarySchemaV1 = "aerorig.http_summary.v1"

type LatencySummary struct {
	MinMS float64 `json:"min_ms"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	MaxMS float64 `json:"max_ms"`
	AvgMS float64 `json:"avg_ms"`
}

type CountByString struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type CountByStatus struct {
	StatusCode int `json:"status_code"`
	Count      int `json:"count"`
}

type HTTPSummary struct {
	SchemaVersion string          `json:"schema_version"`
	Probe         string          `json:"probe"`
	SourceFiles   []string        `json:"source_files"`
	TotalSamples  int             `json:"total_samples"`
	OKSamples     int             `json:"ok_samples"`
	FailedSamples int             `json:"failed_samples"`
	SuccessRate   float64         `json:"success_rate"`
	LatencyMS     LatencySummary  `json:"latency_ms"`
	StatusCodes   []CountByStatus `json:"status_codes"`
	Errors        []CountByString `json:"errors"`
}

func SummarizeHTTP(paths []string) (HTTPSummary, error) {
	s := HTTPSummary{
		SchemaVersion: HTTPSummarySchemaV1,
		Probe:         "http_summary",
		SourceFiles:   paths,
	}

	var durations []float64
	statusCounts := map[int]int{}
	errorCounts := map[string]int{}

	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}

		if err := readHTTPResults(path, func(r probe.HTTPResult) {
			s.TotalSamples++
			durations = append(durations, r.DurationMS)

			if r.OK {
				s.OKSamples++
			} else {
				s.FailedSamples++
				errText := strings.TrimSpace(r.Error)
				if errText == "" {
					errText = "unknown error"
				}
				errorCounts[errText]++
			}

			if r.StatusCode > 0 {
				statusCounts[r.StatusCode]++
			}
		}); err != nil {
			return s, err
		}
	}

	if s.TotalSamples > 0 {
		s.SuccessRate = float64(s.OKSamples) / float64(s.TotalSamples)
	}

	s.LatencyMS = summarizeDurations(durations)
	s.StatusCodes = sortedStatusCounts(statusCounts)
	s.Errors = sortedStringCounts(errorCounts)

	return s, nil
}

func readHTTPResults(path string, visit func(probe.HTTPResult)) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var r probe.HTTPResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return fmt.Errorf("parse %s line %d: %w", path, lineNo, err)
		}

		visit(r)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}

	return nil
}

func summarizeDurations(values []float64) LatencySummary {
	if len(values) == 0 {
		return LatencySummary{}
	}

	cp := append([]float64(nil), values...)
	sort.Float64s(cp)

	var sum float64
	for _, v := range cp {
		sum += v
	}

	return LatencySummary{
		MinMS: round3(cp[0]),
		P50MS: round3(percentileNearestRank(cp, 0.50)),
		P95MS: round3(percentileNearestRank(cp, 0.95)),
		MaxMS: round3(cp[len(cp)-1]),
		AvgMS: round3(sum / float64(len(cp))),
	}
}

func percentileNearestRank(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}

	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}

	return sorted[rank-1]
}

func sortedStatusCounts(m map[int]int) []CountByStatus {
	out := make([]CountByStatus, 0, len(m))
	for status, count := range m {
		out = append(out, CountByStatus{StatusCode: status, Count: count})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StatusCode < out[j].StatusCode
	})

	return out
}

func sortedStringCounts(m map[string]int) []CountByString {
	out := make([]CountByString, 0, len(m))
	for value, count := range m {
		out = append(out, CountByString{Value: value, Count: count})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Value < out[j].Value
		}
		return out[i].Count > out[j].Count
	})

	return out
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
