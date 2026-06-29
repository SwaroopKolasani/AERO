package l3r2

import (
	"context"

	"aero-cache/internal/store"
)

type DisabledStore struct{}

func NewDisabled() DisabledStore {
	return DisabledStore{}
}

func (DisabledStore) Name() string {
	return "l3"
}

func (DisabledStore) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	return nil, false, nil
}

func (DisabledStore) Put(ctx context.Context, key store.Key, e *store.Entry) error {
	return nil
}

func (DisabledStore) Delete(ctx context.Context, key store.Key) error {
	return nil
}
