package key

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type llama3ParityCase struct {
	Model          string           `json:"model"`
	Messages       []map[string]any `json:"messages"`
	ExpectedTokens []uint32         `json:"expected_tokens"`
	ExpectedCount  int              `json:"expected_count"`
}

func TestLlama3TokenizerParityAgainstHFReference(t *testing.T) {
	tokenizerDir := os.Getenv("AERO_TOKENIZER_DIR")
	if tokenizerDir == "" {
		t.Skip("set AERO_TOKENIZER_DIR to run tokenizer parity test")
	}

	caseGlob := os.Getenv("AERO_PARITY_CASE_GLOB")
	if caseGlob == "" {
		caseGlob = "../../tools/tokenizer_parity/cases/*.json"
	}

	chatTemplateDate := os.Getenv("AERO_CHAT_TEMPLATE_DATE")
	if chatTemplateDate == "" {
		chatTemplateDate = "04 Jul 2026"
	}

	casePaths, err := filepath.Glob(caseGlob)
	if err != nil {
		t.Fatalf("bad parity case glob %q: %v", caseGlob, err)
	}

	if len(casePaths) == 0 {
		t.Skipf("no parity fixtures found at %s", caseGlob)
	}

	bundle, err := LoadTokenizerBundle(TokenizerBundleConfig{
		Dir:              tokenizerDir,
		ChatTemplateKind: "llama3",
		ChatTemplateDate: chatTemplateDate,
	})
	if err != nil {
		t.Fatalf("load tokenizer bundle: %v", err)
	}

	for _, casePath := range casePaths {
		t.Run(filepath.Base(casePath), func(t *testing.T) {
			raw, err := os.ReadFile(casePath)
			if err != nil {
				t.Fatalf("read parity case: %v", err)
			}

			var tc llama3ParityCase
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()

			if err := dec.Decode(&tc); err != nil {
				t.Fatalf("decode parity case: %v", err)
			}

			if tc.ExpectedCount == 0 || len(tc.ExpectedTokens) == 0 {
				t.Fatalf("fixture has no expected tokens; regenerate with hf_reference.py --write")
			}

			if tc.ExpectedCount != len(tc.ExpectedTokens) {
				t.Fatalf(
					"fixture expected_count=%d but expected_tokens length=%d",
					tc.ExpectedCount,
					len(tc.ExpectedTokens),
				)
			}

			msgs := make([]any, len(tc.Messages))
			for i := range tc.Messages {
				msgs[i] = tc.Messages[i]
			}

			req := map[string]any{
				"model":       tc.Model,
				"messages":    msgs,
				"temperature": int64(0),
			}

			rendered, err := bundle.Renderer.Render(req)
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			got, err := bundle.Tokenizer.Tokenize(rendered)
			if err != nil {
				t.Fatalf("tokenize: %v", err)
			}

			if len(got) != tc.ExpectedCount {
				t.Fatalf(
					"token count mismatch: got=%d want=%d\nrendered:\n%s\ngot=%v\nwant=%v",
					len(got),
					tc.ExpectedCount,
					rendered,
					got,
					tc.ExpectedTokens,
				)
			}

			if !reflect.DeepEqual(got, tc.ExpectedTokens) {
				t.Fatalf("token ids mismatch\nrendered:\n%s\ngot=%v\nwant=%v", rendered, got, tc.ExpectedTokens)
			}
		})
	}
}