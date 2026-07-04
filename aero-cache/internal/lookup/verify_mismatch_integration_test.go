package lookup_test

import (
	"context"
	"testing"
	"time"

	"aero-cache/internal/key"
	"aero-cache/internal/lookup"
	"aero-cache/internal/metrics"
	"aero-cache/internal/store"
)

func TestLookupRejectsAndDeletesResponseHashMismatch(t *testing.T) {
	ctx := context.Background()

	material := &key.Material{
		KeyHex:          "deadbeef",
		StoreKey:        "aero:test:fingerprint:7:deadbeef",
		TokenIDs:        []uint32{128000, 128006, 882, 128007},
		CanonicalParams: []byte(`{"model":"test-model","temperature":0}`),
		Fingerprint:     "test-fingerprint",
		Epoch:           7,
	}

	badEntry := &store.Entry{
		TokenIDs:    append([]uint32(nil), material.TokenIDs...),
		Params:      append([]byte(nil), material.CanonicalParams...),
		Fingerprint: material.Fingerprint,
		Epoch:       material.Epoch,

		Response:   []byte(`{"this":"is corrupted relative to RespHash"}`),
		RespHash:   [32]byte{}, // Intentionally wrong.
		Compressed: false,

		CreatedAt:   time.Now().Unix(),
		TTL:         time.Hour,
		TokensOut:   3,
		OriginTier:  "dev",
		StatusCode:  200,
		ContentType: "application/json",
	}

	st := newRecordingStore("cache-l1")
	if err := st.Put(ctx, store.Key(material.StoreKey), badEntry); err != nil {
		t.Fatalf("put bad entry: %v", err)
	}

	reg := metrics.NewRegistry()

	orchestrator := lookup.New([]lookup.Tier{
		{
			Store:  st,
			Budget: time.Second,
		},
	}, reg)

	res := orchestrator.Lookup(ctx, material)

	if res.Hit {
		t.Fatalf("expected corrupted entry to be rejected, got hit tier=%q", res.Tier)
	}

	if !st.deleted {
		t.Fatalf("expected verify mismatch to delete bad entry")
	}

	if st.deletedKey != store.Key(material.StoreKey) {
		t.Fatalf("deleted key=%q, want %q", st.deletedKey, store.Key(material.StoreKey))
	}

	if _, ok, err := st.Get(ctx, store.Key(material.StoreKey)); err != nil {
		t.Fatalf("get after delete: %v", err)
	} else if ok {
		t.Fatalf("bad entry still exists after verify mismatch")
	}
}

func TestLookupRejectsAndDeletesRequestMaterialMismatch(t *testing.T) {
	ctx := context.Background()

	material := &key.Material{
		KeyHex:          "cafebabe",
		StoreKey:        "aero:test:fingerprint:9:cafebabe",
		TokenIDs:        []uint32{1, 2, 3, 4},
		CanonicalParams: []byte(`{"model":"test-model","temperature":0}`),
		Fingerprint:     "test-fingerprint",
		Epoch:           9,
	}

	// RespHash correctness is irrelevant here because request-side verification
	// must fail first due to mismatched token IDs.
	entryWithWrongRequestMaterial := &store.Entry{
		TokenIDs:    []uint32{9, 9, 9, 9},
		Params:      append([]byte(nil), material.CanonicalParams...),
		Fingerprint: material.Fingerprint,
		Epoch:       material.Epoch,

		Response:   []byte(`{"ok":true}`),
		RespHash:   [32]byte{},
		Compressed: false,

		CreatedAt:   time.Now().Unix(),
		TTL:         time.Hour,
		TokensOut:   1,
		OriginTier:  "dev",
		StatusCode:  200,
		ContentType: "application/json",
	}

	st := newRecordingStore("cache-l1")
	if err := st.Put(ctx, store.Key(material.StoreKey), entryWithWrongRequestMaterial); err != nil {
		t.Fatalf("put bad entry: %v", err)
	}

	reg := metrics.NewRegistry()

	orchestrator := lookup.New([]lookup.Tier{
		{
			Store:  st,
			Budget: time.Second,
		},
	}, reg)

	res := orchestrator.Lookup(ctx, material)

	if res.Hit {
		t.Fatalf("expected request-material mismatch to be rejected, got hit tier=%q", res.Tier)
	}

	if !st.deleted {
		t.Fatalf("expected request-material mismatch to delete bad entry")
	}

	if st.deletedKey != store.Key(material.StoreKey) {
		t.Fatalf("deleted key=%q, want %q", st.deletedKey, store.Key(material.StoreKey))
	}
}

type recordingStore struct {
	name string
	data map[store.Key]*store.Entry

	deleted    bool
	deletedKey store.Key
}

func newRecordingStore(name string) *recordingStore {
	return &recordingStore{
		name: name,
		data: make(map[store.Key]*store.Entry),
	}
}

func (s *recordingStore) Name() string {
	return s.name
}

func (s *recordingStore) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	e, ok := s.data[key]
	if !ok {
		return nil, false, nil
	}

	return cloneEntry(e), true, nil
}

func (s *recordingStore) Put(ctx context.Context, key store.Key, e *store.Entry) error {
	s.data[key] = cloneEntry(e)
	return nil
}

func (s *recordingStore) Delete(ctx context.Context, key store.Key) error {
	s.deleted = true
	s.deletedKey = key
	delete(s.data, key)
	return nil
}

func cloneEntry(e *store.Entry) *store.Entry {
	if e == nil {
		return nil
	}

	out := *e

	out.TokenIDs = append([]uint32(nil), e.TokenIDs...)
	out.Params = append([]byte(nil), e.Params...)
	out.Response = append([]byte(nil), e.Response...)

	return &out
}
