// not a final tokenizer just to preserve the api and make the key builder test deterministic

package key

type ByteTokenizer struct{}

func (ByteTokenizer) Tokenize(renderedPrompt string) ([]uint32, error) {
	b := []byte(renderedPrompt)
	out := make([]uint32, len(b))
	for i, x := range b {
		out[i] = uint32(x)
	}
	return out, nil
}
