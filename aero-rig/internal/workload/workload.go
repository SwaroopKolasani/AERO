package workload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aero-rig/internal/suite"
)

const SchemaV1 = "aerorig.workload.v1"

type Manifest struct {
	SchemaVersion string       `json:"schema_version,omitempty"`
	Name          string       `json:"name"`
	OutputDir     string       `json:"output_dir,omitempty"`
	Targets       []TargetSpec `json:"targets"`
	Cases         []CaseSpec   `json:"cases"`
}

type TargetSpec struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Model     string `json:"model"`
	Count     int    `json:"count,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

type CaseSpec struct {
	Name       string           `json:"name"`
	Prompt     string           `json:"prompt,omitempty"`
	PromptFile string           `json:"prompt_file,omitempty"`
	Count      int              `json:"count,omitempty"`
	Timeout    string           `json:"timeout,omitempty"`
	Proof      *suite.ProofSpec `json:"proof,omitempty"`
}

func Load(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read workload manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse workload manifest: %w", err)
	}

	if err := Validate(m); err != nil {
		return Manifest{}, err
	}

	return m, nil
}

func Validate(m Manifest) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("workload name is required")
	}
	if len(m.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}
	if len(m.Cases) == 0 {
		return fmt.Errorf("at least one case is required")
	}

	for i, target := range m.Targets {
		prefix := fmt.Sprintf("target[%d]", i)

		if strings.TrimSpace(target.Name) == "" {
			return fmt.Errorf("%s name is required", prefix)
		}
		switch strings.TrimSpace(target.Kind) {
		case "chat", "chat_stream", "both":
		default:
			return fmt.Errorf("%s kind must be chat, chat_stream, or both", prefix)
		}
		if strings.TrimSpace(target.URL) == "" {
			return fmt.Errorf("%s url is required", prefix)
		}
		if strings.TrimSpace(target.Model) == "" {
			return fmt.Errorf("%s model is required", prefix)
		}
	}

	for i, c := range m.Cases {
		prefix := fmt.Sprintf("case[%d]", i)

		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("%s name is required", prefix)
		}
		if strings.TrimSpace(c.Prompt) == "" && strings.TrimSpace(c.PromptFile) == "" {
			return fmt.Errorf("%s prompt or prompt_file is required", prefix)
		}
	}

	return nil
}

func BuildSuite(m Manifest) suite.Manifest {
	outDir := strings.TrimSpace(m.OutputDir)
	if outDir == "" {
		outDir = filepath.Join("out", "suites", sanitizeName(m.Name))
	}

	s := suite.Manifest{
		Name:      m.Name,
		OutputDir: outDir,
	}

	for _, target := range m.Targets {
		for _, c := range m.Cases {
			count := firstPositive(c.Count, target.Count, 1)
			timeout := firstNonEmpty(c.Timeout, target.Timeout, defaultTimeoutForKind(target.Kind))
			name := sanitizeName(target.Name + "-" + c.Name)

			switch strings.TrimSpace(target.Kind) {
			case "chat":
				s.Chat = append(s.Chat, buildChatSpec(name, target, c, count, timeout))
			case "chat_stream":
				s.ChatStream = append(s.ChatStream, buildChatStreamSpec(name, target, c, count, timeout))
			case "both":
				s.Chat = append(s.Chat, buildChatSpec(name+"-chat", target, c, count, timeout))
				s.ChatStream = append(s.ChatStream, buildChatStreamSpec(name+"-stream", target, c, count, timeout))
			}
		}
	}

	return s
}

func WriteSuite(path string, s suite.Manifest) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create suite manifest: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("write suite manifest: %w", err)
	}

	return nil
}

func buildChatSpec(name string, target TargetSpec, c CaseSpec, count int, timeout string) suite.ChatProbeSpec {
	return suite.ChatProbeSpec{
		Name:       name,
		Target:     target.URL,
		Model:      target.Model,
		Prompt:     c.Prompt,
		PromptFile: c.PromptFile,
		Count:      count,
		Timeout:    timeout,
		APIKeyEnv:  target.APIKeyEnv,
		Proof:      c.Proof,
	}
}

func buildChatStreamSpec(name string, target TargetSpec, c CaseSpec, count int, timeout string) suite.ChatStreamProbeSpec {
	return suite.ChatStreamProbeSpec{
		Name:       name,
		Target:     target.URL,
		Model:      target.Model,
		Prompt:     c.Prompt,
		PromptFile: c.PromptFile,
		Count:      count,
		Timeout:    timeout,
		APIKeyEnv:  target.APIKeyEnv,
	}
}

func defaultTimeoutForKind(kind string) string {
	if strings.TrimSpace(kind) == "chat_stream" {
		return "60s"
	}
	return "30s"
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
