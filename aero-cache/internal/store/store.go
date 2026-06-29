package store

import (
	"context"
	"time"
)

type Key string

type Store interface {
	Get(ctx context.Context, key Key) (*Entry, bool, error)
	Put(ctx context.Context, key Key, e *Entry) error
	Delete(ctx context.Context, key Key) error
	Name() string
}

type Entry struct {
	TokenIDs    []uint32      `json:"token_ids"`
	Params      []byte        `json:"params"`
	Fingerprint string        `json:"fingerprint"`
	Epoch       uint64        `json:"epoch"`
	Response    []byte        `json:"response"`
	RespHash    [32]byte      `json:"resp_hash"`
	Compressed  bool          `json:"compressed"`
	DictID      uint32        `json:"dict_id"`
	CreatedAt   int64         `json:"created_at"`
	TTL         time.Duration `json:"ttl"`
	TokensOut   int           `json:"tokens_out"`
	OriginTier  string        `json:"origin_tier"`
}

func (e *Entry) Clone() *Entry {
	if e == nil {
		return nil
	}

	out := *e

	if e.TokenIDs != nil {
		out.TokenIDs = append([]uint32(nil), e.TokenIDs...)
	}
	if e.Params != nil {
		out.Params = append([]byte(nil), e.Params...)
	}
	if e.Response != nil {
		out.Response = append([]byte(nil), e.Response...)
	}

	return &out
}

func (e *Entry) SizeBytes() int64 {
	if e == nil {
		return 0
	}

	return int64(
		len(e.TokenIDs)*4 +
			len(e.Params) +
			len(e.Fingerprint) +
			len(e.Response) +
			32 +
			128,
	)
}
