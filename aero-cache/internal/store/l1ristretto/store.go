package l1ristretto

import (
	"context"
	"errors"
	"time"

	"aero-cache/internal/store"

	"github.com/dgraph-io/ristretto"
)

type Config struct {
	MaxBytes      int64
	MaxEntryBytes int64
	NumCounters   int64
	TTL           time.Duration
}

type Store struct {
	cache         *ristretto.Cache
	maxEntryBytes int64
	ttl           time.Duration
}

func New(cfg Config) (*Store, error) {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 512 << 20
	}
	if cfg.MaxEntryBytes <= 0 {
		cfg.MaxEntryBytes = 256 << 10
	}
	if cfg.NumCounters <= 0 {
		cfg.NumCounters = 1_000_000
	}
	if cfg.TTL <= 0 {
		cfg.TTL = time.Hour
	}

	c, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: cfg.NumCounters,
		MaxCost:     cfg.MaxBytes,
		BufferItems: 64,
	})
	if err != nil {
		return nil, err
	}

	return &Store{
		cache:         c,
		maxEntryBytes: cfg.MaxEntryBytes,
		ttl:           cfg.TTL,
	}, nil
}

func (s *Store) Name() string {
	return "l1"
}

func (s *Store) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	default:
	}

	v, ok := s.cache.Get(string(key))
	if !ok {
		return nil, false, nil
	}

	e, ok := v.(*store.Entry)
	if !ok {
		_ = s.Delete(ctx, key)
		return nil, false, errors.New("l1 entry has invalid type")
	}

	return e.Clone(), true, nil
}

func (s *Store) Put(ctx context.Context, key store.Key, e *store.Entry) error {
	if e == nil {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	clone := e.Clone()
	clone.Compressed = false

	cost := clone.SizeBytes()
	if cost > s.maxEntryBytes {
		return nil
	}

	s.cache.SetWithTTL(string(key), clone, cost, s.ttl)
	s.cache.Wait()

	return nil
}

func (s *Store) Delete(ctx context.Context, key store.Key) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.cache.Del(string(key))
	return nil
}
