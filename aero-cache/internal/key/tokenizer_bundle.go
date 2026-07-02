package key

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TokenizerBundleConfig struct {
	Dir                 string
	TokenizerPath       string
	TokenizerConfigPath string
	ChatTemplatePath    string
	ChatTemplateKind    string
}

type TokenizerBundle struct {
	Tokenizer             Tokenizer
	Renderer              Renderer
	TokenizerPath         string
	TokenizerSHA256       string
	TokenizerConfigPath   string
	TokenizerConfigSHA256 string
	ChatTemplatePath      string
	ChatTemplateSHA256    string
	ChatTemplateKind      string
}

func LoadTokenizerBundle(cfg TokenizerBundleConfig) (*TokenizerBundle, error) {
	if cfg.Dir != "" {
		if cfg.TokenizerPath == "" {
			cfg.TokenizerPath = filepath.Join(cfg.Dir, "tokenizer.json")
		}
		if cfg.TokenizerConfigPath == "" {
			cfg.TokenizerConfigPath = filepath.Join(cfg.Dir, "tokenizer_config.json")
		}
		if cfg.ChatTemplatePath == "" {
			cfg.ChatTemplatePath = filepath.Join(cfg.Dir, "chat_template.jinja")
		}
	}

	if cfg.TokenizerPath == "" {
		return nil, fmt.Errorf("tokenizer path is required")
	}

	tokenizerHash, err := fileSHA256(cfg.TokenizerPath)
	if err != nil {
		return nil, err
	}

	tok, err := NewHuggingFaceTokenizer(cfg.TokenizerPath)
	if err != nil {
		return nil, err
	}

	templateText := ""
	templateHash := ""
	templatePath := ""

	if cfg.ChatTemplatePath != "" {
		if b, err := os.ReadFile(cfg.ChatTemplatePath); err == nil {
			templateText = string(b)
			templateHash = sha256Hex(b)
			templatePath = cfg.ChatTemplatePath
		}
	}

	configHash := ""
	if cfg.TokenizerConfigPath != "" {
		if b, err := os.ReadFile(cfg.TokenizerConfigPath); err == nil {
			configHash = sha256Hex(b)

			if templateText == "" {
				if extracted := extractChatTemplate(b); extracted != "" {
					templateText = extracted
					templateHash = sha256Hex([]byte(extracted))
					templatePath = cfg.TokenizerConfigPath + "#chat_template"
				}
			}
		}
	}

	kind := strings.TrimSpace(cfg.ChatTemplateKind)
	if kind == "" {
		kind = detectTemplateKind(templateText)
	}

	var renderer Renderer

	switch kind {
	case "llama3":
		renderer = Llama3Renderer{}
	default:
		return nil, fmt.Errorf("unsupported chat template kind %q", kind)
	}

	return &TokenizerBundle{
		Tokenizer:             tok,
		Renderer:              renderer,
		TokenizerPath:         cfg.TokenizerPath,
		TokenizerSHA256:       tokenizerHash,
		TokenizerConfigPath:   cfg.TokenizerConfigPath,
		TokenizerConfigSHA256: configHash,
		ChatTemplatePath:      templatePath,
		ChatTemplateSHA256:    templateHash,
		ChatTemplateKind:      kind,
	}, nil
}

func extractChatTemplate(b []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return ""
	}

	v, ok := obj["chat_template"].(string)
	if !ok {
		return ""
	}

	return v
}

func detectTemplateKind(template string) string {
	if strings.Contains(template, "<|start_header_id|>") &&
		strings.Contains(template, "<|end_header_id|>") &&
		strings.Contains(template, "<|eot_id|>") {
		return "llama3"
	}

	return ""
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return sha256Hex(b), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
