package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aero-rig/internal/probe"
	"aero-rig/internal/report"
)

const ResultSchemaV1 = "aerorig.suite_result.v1"

type Manifest struct {
	Name      string          `json:"name"`
	OutputDir string          `json:"output_dir,omitempty"`
	HTTP      []HTTPProbeSpec `json:"http,omitempty"`
	Chat      []ChatProbeSpec `json:"chat,omitempty"`
}

type HTTPProbeSpec struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Method  string `json:"method,omitempty"`
	Count   int    `json:"count,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

type ChatProbeSpec struct {
	Name       string     `json:"name"`
	Target     string     `json:"target"`
	Model      string     `json:"model"`
	Prompt     string     `json:"prompt,omitempty"`
	PromptFile string     `json:"prompt_file,omitempty"`
	Count      int        `json:"count,omitempty"`
	Timeout    string     `json:"timeout,omitempty"`
	APIKeyEnv  string     `json:"api_key_env,omitempty"`
	Proof      *ProofSpec `json:"proof,omitempty"`
}

type ProofSpec struct {
	RequireCacheHit    bool `json:"require_cache_hit,omitempty"`
	RequireVerifiedHit bool `json:"require_verified_hit,omitempty"`
	RequireMissHit     bool `json:"require_miss_hit,omitempty"`
}

type Artifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type SuiteResult struct {
	SchemaVersion string     `json:"schema_version"`
	SuiteName     string     `json:"suite_name"`
	OutputDir     string     `json:"output_dir"`
	StartedAt     string     `json:"started_at"`
	DurationMS    float64    `json:"duration_ms"`
	Passed        bool       `json:"passed"`
	HTTPArtifacts []Artifact `json:"http_artifacts,omitempty"`
	ChatArtifacts []Artifact `json:"chat_artifacts,omitempty"`
	Errors        []string   `json:"errors,omitempty"`
}

func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}

	if strings.TrimSpace(m.Name) == "" {
		return Manifest{}, fmt.Errorf("manifest name is required")
	}

	return m, nil
}

func Run(ctx context.Context, m Manifest) (SuiteResult, error) {
	start := time.Now()
	outDir := strings.TrimSpace(m.OutputDir)
	if outDir == "" {
		outDir = filepath.Join("out", "suites", sanitizeName(m.Name))
	}

	res := SuiteResult{
		SchemaVersion: ResultSchemaV1,
		SuiteName:     m.Name,
		OutputDir:     outDir,
		StartedAt:     start.UTC().Format(time.RFC3339Nano),
		Passed:        true,
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return res, fmt.Errorf("create output dir: %w", err)
	}

	runID := fmt.Sprintf("%d-%d", time.Now().UTC().UnixNano(), os.Getpid())

	for _, spec := range m.HTTP {
		artifacts, ok := runHTTPSpec(ctx, outDir, runID, spec, &res)
		res.HTTPArtifacts = append(res.HTTPArtifacts, artifacts...)
		if !ok {
			res.Passed = false
		}
	}

	for _, spec := range m.Chat {
		artifacts, ok := runChatSpec(ctx, outDir, runID, spec, &res)
		res.ChatArtifacts = append(res.ChatArtifacts, artifacts...)
		if !ok {
			res.Passed = false
		}
	}

	res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
	resultPath := filepath.Join(outDir, "suite_result.json")
	if err := writeJSON(resultPath, res); err != nil {
		return res, err
	}

	return res, nil
}

func runHTTPSpec(ctx context.Context, outDir string, runID string, spec HTTPProbeSpec, suiteResult *SuiteResult) ([]Artifact, bool) {
	name := sanitizeName(defaultString(spec.Name, "http"))
	count := defaultInt(spec.Count, 1)
	timeout := parseDurationDefault(spec.Timeout, 2*time.Second)
	method := defaultString(spec.Method, http.MethodGet)

	rawPath := filepath.Join(outDir, name+".http.jsonl")
	summaryPath := filepath.Join(outDir, name+".http.summary.json")

	f, err := os.Create(rawPath)
	if err != nil {
		suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: create raw output: %v", spec.Name, err))
		return nil, false
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	client := &http.Client{Timeout: timeout}
	ok := true

	for i := 1; i <= count; i++ {
		sampleCtx, cancel := context.WithTimeout(ctx, timeout)
		r := probe.HTTP(sampleCtx, client, probe.HTTPRequest{
			RunID:      runID,
			Sample:     i,
			TargetName: spec.Name,
			TargetURL:  spec.Target,
			Method:     method,
		})
		cancel()

		if err := enc.Encode(r); err != nil {
			suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: write raw output: %v", spec.Name, err))
			return []Artifact{{Name: spec.Name + " raw", Path: rawPath}}, false
		}
		if !r.OK {
			ok = false
		}
	}

	summary, err := report.SummarizeHTTP([]string{rawPath})
	if err != nil {
		suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: summarize http: %v", spec.Name, err))
		return []Artifact{{Name: spec.Name + " raw", Path: rawPath}}, false
	}
	if err := writeJSON(summaryPath, summary); err != nil {
		suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: write http summary: %v", spec.Name, err))
		return []Artifact{{Name: spec.Name + " raw", Path: rawPath}}, false
	}
	if summary.FailedSamples > 0 {
		ok = false
	}

	return []Artifact{
		{Name: spec.Name + " raw", Path: rawPath},
		{Name: spec.Name + " summary", Path: summaryPath},
	}, ok
}

func runChatSpec(ctx context.Context, outDir string, runID string, spec ChatProbeSpec, suiteResult *SuiteResult) ([]Artifact, bool) {
	name := sanitizeName(defaultString(spec.Name, "chat"))
	count := defaultInt(spec.Count, 1)
	timeout := parseDurationDefault(spec.Timeout, 30*time.Second)

	rawPath := filepath.Join(outDir, name+".chat.jsonl")
	summaryPath := filepath.Join(outDir, name+".chat.summary.json")
	proofPath := filepath.Join(outDir, name+".chat.proof.json")

	prompt := spec.Prompt
	if strings.TrimSpace(spec.PromptFile) != "" {
		b, err := os.ReadFile(spec.PromptFile)
		if err != nil {
			suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: read prompt file: %v", spec.Name, err))
			return nil, false
		}
		prompt = string(b)
	}

	apiKey := ""
	if strings.TrimSpace(spec.APIKeyEnv) != "" {
		apiKey = os.Getenv(strings.TrimSpace(spec.APIKeyEnv))
	}

	f, err := os.Create(rawPath)
	if err != nil {
		suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: create raw output: %v", spec.Name, err))
		return nil, false
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	client := &http.Client{Timeout: timeout}
	ok := true

	for i := 1; i <= count; i++ {
		sampleCtx, cancel := context.WithTimeout(ctx, timeout)
		r := probe.Chat(sampleCtx, client, probe.ChatRequest{
			RunID:      runID,
			Sample:     i,
			TargetName: spec.Name,
			TargetURL:  spec.Target,
			Model:      spec.Model,
			Prompt:     prompt,
			APIKey:     apiKey,
			Timeout:    timeout,
		})
		cancel()

		if err := enc.Encode(r); err != nil {
			suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: write raw output: %v", spec.Name, err))
			return []Artifact{{Name: spec.Name + " raw", Path: rawPath}}, false
		}
		if !r.OK {
			ok = false
		}
	}

	summary, err := report.SummarizeChat([]string{rawPath})
	if err != nil {
		suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: summarize chat: %v", spec.Name, err))
		return []Artifact{{Name: spec.Name + " raw", Path: rawPath}}, false
	}
	if err := writeJSON(summaryPath, summary); err != nil {
		suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: write chat summary: %v", spec.Name, err))
		return []Artifact{{Name: spec.Name + " raw", Path: rawPath}}, false
	}
	if summary.FailedSamples > 0 {
		ok = false
	}

	artifacts := []Artifact{
		{Name: spec.Name + " raw", Path: rawPath},
		{Name: spec.Name + " summary", Path: summaryPath},
	}

	if spec.Proof != nil {
		proof, err := report.BuildChatProof([]string{rawPath}, report.ChatProofOptions{
			RequireCacheHit:    spec.Proof.RequireCacheHit,
			RequireVerifiedHit: spec.Proof.RequireVerifiedHit,
			RequireMissThenHit: spec.Proof.RequireMissHit,
		})
		if err != nil {
			suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: build chat proof: %v", spec.Name, err))
			return artifacts, false
		}
		if err := writeJSON(proofPath, proof); err != nil {
			suiteResult.Errors = append(suiteResult.Errors, fmt.Sprintf("%s: write chat proof: %v", spec.Name, err))
			return artifacts, false
		}
		artifacts = append(artifacts, Artifact{Name: spec.Name + " proof", Path: proofPath})
		if !proof.Passed {
			ok = false
		}
	}

	return artifacts, ok
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "unnamed"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}

		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}

func defaultString(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func defaultInt(v int, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func parseDurationDefault(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
