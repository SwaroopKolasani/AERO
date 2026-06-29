package memstore

import (
	"context"
	"sync"

	"aero-cache/internal/store"
)

type Store struct {
	name string
	mu   sync.RWMutex
	data map[store.Key]*store.Entry
	err  error
}

func New(name string) *Store {
	return &Store{
		name: name,
		data: map[store.Key]*store.Entry{},
	}
}

func (s *Store) Name() string {
	return s.name
}

func (s *Store) SetError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *Store) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	default:
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.err != nil {
		return nil, false, s.err
	}

	e, ok := s.data[key]
	if !ok {
		return nil, false, nil
	}

	return e.Clone(), true, nil
}

func (s *Store) Put(ctx context.Context, key store.Key, e *store.Entry) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}

	s.data[key] = e.Clone()
	return nil
}

func (s *Store) Delete(ctx context.Context, key store.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)
	return nil
}
