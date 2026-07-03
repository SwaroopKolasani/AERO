package key

import (
	"fmt"

	llama3 "github.com/agentstation/tokenizer/llama3"
)

type Llama3Tokenizer struct {
	tok *llama3.Tokenizer
}

// tokenizerModelPath is intentionally ignored for this implementation.
// github.com/agentstation/tokenizer/llama3 loads its default Llama 3 tokenizer data via llama3.New().
func NewLlama3Tokenizer(tokenizerModelPath string) (*Llama3Tokenizer, error) {
	tok, err := llama3.New()
	if err != nil {
		return nil, fmt.Errorf("load llama3 tokenizer: %w", err)
	}

	return &Llama3Tokenizer{tok: tok}, nil
}

func (t *Llama3Tokenizer) Tokenize(renderedPrompt string) ([]uint32, error) {
	ids := t.tok.Encode(renderedPrompt, &llama3.EncodeOptions{
		BOS: false,
		EOS: false,
	})

	out := make([]uint32, len(ids))
	for i, id := range ids {
		if id < 0 {
			return nil, fmt.Errorf("tokenizer returned negative token id %d", id)
		}
		out[i] = uint32(id)
	}

	return out, nil
}
