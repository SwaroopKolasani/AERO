package key

import (
	"fmt"

	sp "github.com/tggo/goSentencePiece"
)

type HuggingFaceTokenizer struct {
	tok *sp.Tokenizer
}

func NewHuggingFaceTokenizer(tokenizerJSONPath string) (*HuggingFaceTokenizer, error) {
	tok, err := sp.NewTokenizerFromJSON(tokenizerJSONPath)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer.json: %w", err)
	}

	return &HuggingFaceTokenizer{tok: tok}, nil
}

func (t *HuggingFaceTokenizer) Tokenize(renderedPrompt string) ([]uint32, error) {
	ids, err := t.tok.Encode(renderedPrompt)
	if err != nil {
		return nil, err
	}

	out := make([]uint32, len(ids))
	for i, id := range ids {
		if id < 0 {
			return nil, fmt.Errorf("tokenizer returned negative token id %d", id)
		}
		out[i] = uint32(id)
	}

	return out, nil
}
