package key

import (
	"bytes"
	"testing"
)

func testBuilder(t *testing.T) *Builder {
	t.Helper()

	b, err := NewBuilder(BuilderConfig{
		Fingerprint: Fingerprint{
			Model:  "dev/tiny@local",
			Engine: "ollama@local",
			Config: map[string]any{
				"dtype": "cpu",
				"tp":    1,
			},
		},
		Epoch:     0,
		Tokenizer: ByteTokenizer{},
	})
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestStructurallyIdenticalJSONProducesSameKey(t *testing.T) {
	b := testBuilder(t)

	a := []byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "say pong"}],
		"temperature": 0,
		"max_tokens": 16,
		"stream": false
	}`)

	z := []byte(`{"stream":false,"max_tokens":16,"temperature":0,"messages":[{"content":"say pong","role":"user"}],"model":"tiny"}`)

	ka, err := b.Build(a)
	if err != nil {
		t.Fatal(err)
	}

	kz, err := b.Build(z)
	if err != nil {
		t.Fatal(err)
	}

	if ka.KeyHex != kz.KeyHex {
		t.Fatalf("expected same key\n%s\n%s", ka.KeyHex, kz.KeyHex)
	}

	if !bytes.Equal(ka.CanonicalParams, kz.CanonicalParams) {
		t.Fatalf("expected same canonical params\n%s\n%s", ka.CanonicalParams, kz.CanonicalParams)
	}
}

func TestPromptCharacterChangeChangesKey(t *testing.T) {
	b := testBuilder(t)

	a, err := b.Build([]byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "say pong"}],
		"temperature": 0
	}`))
	if err != nil {
		t.Fatal(err)
	}

	z, err := b.Build([]byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "say pong!"}],
		"temperature": 0
	}`))
	if err != nil {
		t.Fatal(err)
	}

	if a.KeyHex == z.KeyHex {
		t.Fatalf("expected prompt mutation to change key: %s", a.KeyHex)
	}
}

func TestMaxTokensChangeChangesKey(t *testing.T) {
	b := testBuilder(t)

	a, err := b.Build([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"max_tokens": 16
	}`))
	if err != nil {
		t.Fatal(err)
	}

	z, err := b.Build([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"max_tokens": 32
	}`))
	if err != nil {
		t.Fatal(err)
	}

	if a.KeyHex == z.KeyHex {
		t.Fatalf("expected max_tokens mutation to change key: %s", a.KeyHex)
	}
}

func TestFingerprintChangeChangesKey(t *testing.T) {
	aBuilder := testBuilder(t)

	zBuilder, err := NewBuilder(BuilderConfig{
		Fingerprint: Fingerprint{
			Model:  "dev/tiny@local",
			Engine: "ollama@new",
			Config: map[string]any{
				"dtype": "cpu",
				"tp":    1,
			},
		},
		Epoch:     0,
		Tokenizer: ByteTokenizer{},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"max_tokens": 16
	}`)

	a, err := aBuilder.Build(body)
	if err != nil {
		t.Fatal(err)
	}

	z, err := zBuilder.Build(body)
	if err != nil {
		t.Fatal(err)
	}

	if a.KeyHex == z.KeyHex {
		t.Fatalf("expected fingerprint mutation to change key: %s", a.KeyHex)
	}
}

func TestEpochChangeChangesKey(t *testing.T) {
	aBuilder := testBuilder(t)

	zBuilder, err := NewBuilder(BuilderConfig{
		Fingerprint: Fingerprint{
			Model:  "dev/tiny@local",
			Engine: "ollama@local",
			Config: map[string]any{
				"dtype": "cpu",
				"tp":    1,
			},
		},
		Epoch:     1,
		Tokenizer: ByteTokenizer{},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"max_tokens": 16
	}`)

	a, err := aBuilder.Build(body)
	if err != nil {
		t.Fatal(err)
	}

	z, err := zBuilder.Build(body)
	if err != nil {
		t.Fatal(err)
	}

	if a.KeyHex == z.KeyHex {
		t.Fatalf("expected epoch mutation to change key: %s", a.KeyHex)
	}
}

func TestNumberNormalizationProducesSameKey(t *testing.T) {
	b := testBuilder(t)

	a, err := b.Build([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"max_tokens": 16
	}`))
	if err != nil {
		t.Fatal(err)
	}

	z, err := b.Build([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0.0,
		"max_tokens": 1.6e1
	}`))
	if err != nil {
		t.Fatal(err)
	}

	if a.KeyHex != z.KeyHex {
		t.Fatalf("expected normalized numbers to produce same key\n%s\n%s", a.KeyHex, z.KeyHex)
	}
}
