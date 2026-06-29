package l2valkey

import (
	"bytes"
	"context"
	"encoding/gob"
	"time"

	"aero-cache/internal/store"

	"github.com/klauspost/compress/zstd"
	"github.com/valkey-io/valkey-go"
)

type Config struct {
	Addr        string
	GetBudget   time.Duration
	TTL         time.Duration
	Compression bool
}

type Store struct {
	client      valkey.Client
	getBudget   time.Duration
	ttl         time.Duration
	compression bool
	enc         *zstd.Encoder
	dec         *zstd.Decoder
}

func New(cfg Config) (*Store, error) {
	if cfg.Addr == "" {
		cfg.Addr = "localhost:6379"
	}
	if cfg.GetBudget <= 0 {
		cfg.GetBudget = 5 * time.Millisecond
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 24 * time.Hour
	}

	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{cfg.Addr},
	})
	if err != nil {
		return nil, err
	}

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		client.Close()
		return nil, err
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		client.Close()
		enc.Close()
		return nil, err
	}

	return &Store{
		client:      client,
		getBudget:   cfg.GetBudget,
		ttl:         cfg.TTL,
		compression: cfg.Compression,
		enc:         enc,
		dec:         dec,
	}, nil
}

func (s *Store) Name() string {
	return "l2"
}

func (s *Store) Close() {
	s.client.Close()
	s.enc.Close()
	s.dec.Close()
}

func (s *Store) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.getBudget)
	defer cancel()

	cmd := s.client.B().Get().Key(string(key)).Build()
	resp := s.client.Do(ctx, cmd)

	b, err := resp.AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	e, err := decodeEntry(b)
	if err != nil {
		return nil, false, err
	}

	if e.Compressed {
		raw, err := s.dec.DecodeAll(e.Response, nil)
		if err != nil {
			return nil, false, err
		}

		e.Response = raw
		e.Compressed = false
	}

	return e, true, nil
}

func (s *Store) Put(ctx context.Context, key store.Key, e *store.Entry) error {
	if e == nil {
		return nil
	}

	clone := e.Clone()

	if s.compression && len(clone.Response) > 0 {
		clone.Response = s.enc.EncodeAll(clone.Response, nil)
		clone.Compressed = true
	}

	payload, err := encodeEntry(clone)
	if err != nil {
		return err
	}

	cmd := s.client.B().
		Set().
		Key(string(key)).
		Value(valkey.BinaryString(payload)).
		Ex(s.ttl).
		Build()

	return s.client.Do(ctx, cmd).Error()
}

func (s *Store) Delete(ctx context.Context, key store.Key) error {
	cmd := s.client.B().Del().Key(string(key)).Build()
	return s.client.Do(ctx, cmd).Error()
}

func encodeEntry(e *store.Entry) ([]byte, error) {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(e); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func decodeEntry(b []byte) (*store.Entry, error) {
	var e store.Entry
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&e); err != nil {
		return nil, err
	}
	return &e, nil
}
