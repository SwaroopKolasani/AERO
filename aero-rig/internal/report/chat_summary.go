package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"aero-rig/internal/probe"
)

const ChatSummarySchemaV1 = "aerorig.chat_summary.v1"

type IntSummary struct {
	Min int     `json:"min"`
	P50 int     `json:"p50"`
	P95 int     `json:"p95"`
	Max int     `json:"max"`
	Avg float64 `json:"avg"`
}

type ChatSummary struct {
	SchemaVersion   string          `json:"schema_version"`
	Probe           string          `json:"probe"`
	SourceFiles     []string        `json:"source_files"`
	TotalSamples    int             `json:"total_samples"`
	OKSamples       int             `json:"ok_samples"`
	FailedSamples   int             `json:"failed_samples"`
	SuccessRate     float64         `json:"success_rate"`
	LatencyMS       LatencySummary  `json:"latency_ms"`
	PromptTokens    IntSummary      `json:"prompt_tokens"`
	OutputTokens    IntSummary      `json:"output_tokens"`
	TotalTokens     IntSummary      `json:"total_tokens"`
	AnswerHashCount int             `json:"answer_hash_count"`
	AnswerStable    bool            `json:"answer_stable"`
	CacheResults    []CountByString `json:"cache_results"`
	Tiers           []CountByString `json:"tiers"`
	VerifiedSamples int             `json:"verified_samples"`
	StatusCodes     []CountByStatus `json:"status_codes"`
	FinishReasons   []CountByString `json:"finish_reasons"`
	ResponseModels  []CountByString `json:"response_models"`
	Errors          []CountByString `json:"errors"`
}

func SummarizeChat(paths []string) (ChatSummary, error) {
	s := ChatSummary{
		SchemaVersion: ChatSummarySchemaV1,
		Probe:         "chat_summary",
		SourceFiles:   paths,
	}

	var durations []float64
	var promptTokens []int
	var outputTokens []int
	var totalTokens []int

	statusCounts := map[int]int{}
	errorCounts := map[string]int{}
	finishReasonCounts := map[string]int{}
	responseModelCounts := map[string]int{}
	cacheCounts := map[string]int{}
	tierCounts := map[string]int{}
	answerHashes := map[string]int{}

	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}

		if err := readChatResults(path, func(r probe.ChatResult) {
			s.TotalSamples++

			if r.DurationMS > 0 {
				durations = append(durations, r.DurationMS)
			}
			if r.StatusCode > 0 {
				statusCounts[r.StatusCode]++
			}

			if r.OK {
				s.OKSamples++

				if r.PromptTokens > 0 {
					promptTokens = append(promptTokens, r.PromptTokens)
				}
				if r.OutputTokens > 0 {
					outputTokens = append(outputTokens, r.OutputTokens)
				}
				if r.TotalTokens > 0 {
					totalTokens = append(totalTokens, r.TotalTokens)
				}
				if strings.TrimSpace(r.AnswerSHA256) != "" {
					answerHashes[r.AnswerSHA256]++
				}
				if strings.TrimSpace(r.FinishReason) != "" {
					finishReasonCounts[r.FinishReason]++
				}
				if strings.TrimSpace(r.ResponseModel) != "" {
					responseModelCounts[r.ResponseModel]++
				}
			} else {
				s.FailedSamples++
				errText := strings.TrimSpace(r.Error)
				if errText == "" {
					errText = "unknown error"
				}
				errorCounts[errText]++
			}

			if v := getAeroHeader(r.AeroHeaders, "X-Aero-Cache"); v != "" {
				cacheCounts[v]++
			}
			if v := getAeroHeader(r.AeroHeaders, "X-Aero-Tier"); v != "" {
				tierCounts[v]++
			}
			if strings.EqualFold(getAeroHeader(r.AeroHeaders, "X-Aero-Verified"), "true") {
				s.VerifiedSamples++
			}
		}); err != nil {
			return s, err
		}
	}

	if s.TotalSamples > 0 {
		s.SuccessRate = float64(s.OKSamples) / float64(s.TotalSamples)
	}

	s.LatencyMS = summarizeDurations(durations)
	s.PromptTokens = summarizeInts(promptTokens)
	s.OutputTokens = summarizeInts(outputTokens)
	s.TotalTokens = summarizeInts(totalTokens)
	s.AnswerHashCount = len(answerHashes)
	s.AnswerStable = s.OKSamples > 0 && len(answerHashes) <= 1
	s.CacheResults = sortedStringCounts(cacheCounts)
	s.Tiers = sortedStringCounts(tierCounts)
	s.StatusCodes = sortedStatusCounts(statusCounts)
	s.FinishReasons = sortedStringCounts(finishReasonCounts)
	s.ResponseModels = sortedStringCounts(responseModelCounts)
	s.Errors = sortedStringCounts(errorCounts)

	return s, nil
}

func readChatResults(path string, visit func(probe.ChatResult)) error {
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

		var r probe.ChatResult
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

func summarizeInts(values []int) IntSummary {
	if len(values) == 0 {
		return IntSummary{}
	}

	cp := append([]int(nil), values...)
	sort.Ints(cp)

	var sum int
	for _, v := range cp {
		sum += v
	}

	return IntSummary{
		Min: cp[0],
		P50: percentileNearestRankInt(cp, 0.50),
		P95: percentileNearestRankInt(cp, 0.95),
		Max: cp[len(cp)-1],
		Avg: round3(float64(sum) / float64(len(cp))),
	}
}

func percentileNearestRankInt(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}

	rank := intCeil(p * float64(len(sorted)))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}

	return sorted[rank-1]
}

func intCeil(v float64) int {
	i := int(v)
	if float64(i) == v {
		return i
	}
	return i + 1
}

func getAeroHeader(headers map[string]string, want string) string {
	for k, v := range headers {
		if strings.EqualFold(k, want) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
