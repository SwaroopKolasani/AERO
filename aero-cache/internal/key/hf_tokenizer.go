package key

import (
	"fmt"
	"path/filepath"
	"strings"

	sp "github.com/tggo/goSentencePiece"
)

type HuggingFaceTokenizer struct {
	tok interface {
		Encode(string) ([]int, error)
	}
}

func NewHuggingFaceTokenizer(tokenizerPath string) (*HuggingFaceTokenizer, error) {
	ext := strings.ToLower(filepath.Ext(tokenizerPath))

	var (
		tok interface {
			Encode(string) ([]int, error)
		}
		err error
	)

	switch ext {
	case ".json":
		tok, err = sp.NewTokenizerFromJSON(tokenizerPath)
	case ".model":
		tok, err = sp.NewTokenizer(tokenizerPath)
	default:
		return nil, fmt.Errorf("unsupported tokenizer format %q; expected tokenizer.json or sentencepiece .model", ext)
	}

	if err != nil {
		return nil, fmt.Errorf("load tokenizer %s: %w", tokenizerPath, err)
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
