package lookup

import (
	"context"
	"errors"
	"testing"
	"time"

	"aero-cache/internal/key"
	"aero-cache/internal/store"
	"aero-cache/internal/store/memstore"

	"lukechampine.com/blake3"
)

type testMetrics struct {
	mismatches int
}

func (m *testMetrics) IncVerifyMismatch() {
	m.mismatches++
}

func materialAndEntry(t *testing.T) (*key.Material, *store.Entry) {
	t.Helper()

	builder, err := key.NewBuilder(key.BuilderConfig{
		Fingerprint: key.Fingerprint{
			Model:  "dev/tiny@local",
			Engine: "ollama@local",
			Config: map[string]any{"dtype": "cpu", "tp": 1},
		},
		Epoch:     0,
		Tokenizer: key.ByteTokenizer{},
	})
	if err != nil {
		t.Fatal(err)
	}

	mat, err := builder.Build([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"max_tokens": 16
	}`))
	if err != nil {
		t.Fatal(err)
	}

	resp := []byte(`{"id":"abc","choices":[{"text":"pong"}]}`)
	hash := blake3.Sum256(resp)

	entry := &store.Entry{
		TokenIDs:    mat.TokenIDs,
		Params:      mat.CanonicalParams,
		Fingerprint: mat.Fingerprint,
		Epoch:       mat.Epoch,
		Response:    resp,
		RespHash:    hash,
		CreatedAt:   time.Now().Unix(),
		TTL:         time.Hour,
		TokensOut:   1,
		OriginTier:  "dev",
	}

	return mat, entry
}

func TestLookupHitsL1(t *testing.T) {
	mat, entry := materialAndEntry(t)

	l1 := memstore.New("l1")
	l2 := memstore.New("l2")

	if err := l1.Put(context.Background(), store.Key(mat.StoreKey), entry); err != nil {
		t.Fatal(err)
	}

	o := New([]Tier{
		{Store: l1},
		{Store: l2},
	}, nil)

	res := o.Lookup(context.Background(), mat)

	if !res.Hit {
		t.Fatalf("expected hit")
	}

	if res.Tier != "cache-l1" {
		t.Fatalf("expected cache-l1, got %s", res.Tier)
	}
}

func TestLookupHitsL2AndPromotesToL1(t *testing.T) {
	mat, entry := materialAndEntry(t)

	l1 := memstore.New("l1")
	l2 := memstore.New("l2")

	if err := l2.Put(context.Background(), store.Key(mat.StoreKey), entry); err != nil {
		t.Fatal(err)
	}

	o := New([]Tier{
		{Store: l1},
		{Store: l2},
	}, nil)

	res := o.Lookup(context.Background(), mat)

	if !res.Hit {
		t.Fatalf("expected hit")
	}

	if res.Tier != "cache-l2" {
		t.Fatalf("expected cache-l2, got %s", res.Tier)
	}

	_, found, err := l1.Get(context.Background(), store.Key(mat.StoreKey))
	if err != nil {
		t.Fatal(err)
	}

	if !found {
		t.Fatalf("expected promotion to l1")
	}
}

func TestVerifyMismatchDeletesBadEntryAndFallsThrough(t *testing.T) {
	mat, entry := materialAndEntry(t)

	bad := entry.Clone()
	bad.Response = []byte(`corrupted-response`)

	l1 := memstore.New("l1")
	l2 := memstore.New("l2")
	m := &testMetrics{}

	if err := l1.Put(context.Background(), store.Key(mat.StoreKey), bad); err != nil {
		t.Fatal(err)
	}
	if err := l2.Put(context.Background(), store.Key(mat.StoreKey), entry); err != nil {
		t.Fatal(err)
	}

	o := New([]Tier{
		{Store: l1},
		{Store: l2},
	}, m)

	res := o.Lookup(context.Background(), mat)

	if !res.Hit {
		t.Fatalf("expected l2 hit after l1 verify mismatch")
	}

	if res.Tier != "cache-l2" {
		t.Fatalf("expected cache-l2, got %s", res.Tier)
	}

	if m.mismatches != 1 {
		t.Fatalf("expected one verify mismatch, got %d", m.mismatches)
	}
}

func TestStoreErrorIsMissNotFailure(t *testing.T) {
	mat, entry := materialAndEntry(t)

	l1 := memstore.New("l1")
	l2 := memstore.New("l2")

	l1.SetError(errors.New("l1 broken"))

	if err := l2.Put(context.Background(), store.Key(mat.StoreKey), entry); err != nil {
		t.Fatal(err)
	}

	o := New([]Tier{
		{Store: l1},
		{Store: l2},
	}, nil)

	res := o.Lookup(context.Background(), mat)

	if !res.Hit {
		t.Fatalf("expected l2 hit after l1 error")
	}

	if res.Tier != "cache-l2" {
		t.Fatalf("expected cache-l2, got %s", res.Tier)
	}
}
