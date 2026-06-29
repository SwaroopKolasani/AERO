package writeback

import (
	"context"
	"time"

	"aero-cache/internal/key"
	"aero-cache/internal/store"

	"lukechampine.com/blake3"
)

type Metrics interface {
	IncWritebackDropped()
	SetWritebackQueueDepth(depth int64)
}

type Job struct {
	Material    *key.Material
	Response    []byte
	StatusCode  int
	ContentType string
	TokensOut   int
	OriginTier  string
	TTL         time.Duration
}

type Queue struct {
	ch      chan Job
	stores  []store.Store
	metrics Metrics
	ttl     time.Duration
}

type Config struct {
	Workers int
	Size    int
	TTL     time.Duration
}

func NewQueue(cfg Config, stores []store.Store, metrics Metrics) *Queue {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Size <= 0 {
		cfg.Size = 1024
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 24 * time.Hour
	}

	q := &Queue{
		ch:      make(chan Job, cfg.Size),
		stores:  stores,
		metrics: metrics,
		ttl:     cfg.TTL,
	}

	for i := 0; i < cfg.Workers; i++ {
		go q.worker()
	}

	return q
}

func (q *Queue) Enqueue(j Job) bool {
	if j.TTL <= 0 {
		j.TTL = q.ttl
	}

	select {
	case q.ch <- j:
		q.setDepth()
		return true
	default:
		if q.metrics != nil {
			q.metrics.IncWritebackDropped()
			q.setDepth()
		}
		return false
	}
}

func (q *Queue) worker() {
	for j := range q.ch {
		q.write(context.Background(), j)
		q.setDepth()
	}
}

func (q *Queue) write(ctx context.Context, j Job) {
	if j.Material == nil || len(j.Response) == 0 {
		return
	}

	status := j.StatusCode
	if status == 0 {
		status = 200
	}

	contentType := j.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	respHash := blake3.Sum256(j.Response)

	entry := &store.Entry{
		TokenIDs:    append([]uint32(nil), j.Material.TokenIDs...),
		Params:      append([]byte(nil), j.Material.CanonicalParams...),
		Fingerprint: j.Material.Fingerprint,
		Epoch:       j.Material.Epoch,

		Response:    append([]byte(nil), j.Response...),
		RespHash:    respHash,
		Compressed:  false,
		DictID:      0,
		StatusCode:  status,
		ContentType: contentType,

		CreatedAt:  time.Now().Unix(),
		TTL:        j.TTL,
		TokensOut:  j.TokensOut,
		OriginTier: j.OriginTier,
	}

	cacheKey := store.Key(j.Material.StoreKey)

	for _, s := range q.stores {
		if s == nil {
			continue
		}

		// Fail-open: write failure is intentionally ignored.
		_ = s.Put(ctx, cacheKey, entry)
	}
}

func (q *Queue) setDepth() {
	if q.metrics != nil {
		q.metrics.SetWritebackQueueDepth(int64(len(q.ch)))
	}
}
