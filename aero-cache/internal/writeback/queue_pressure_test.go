package writeback_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"aero-cache/internal/key"
	"aero-cache/internal/metrics"
	"aero-cache/internal/store"
	"aero-cache/internal/writeback"
)

func TestWritebackQueueDropsWhenFull(t *testing.T) {
	blocker := newBlockingStore("blocking-store")
	reg := metrics.NewRegistry()

	q := writeback.NewQueue(
		writeback.Config{
			Workers: 1,
		},
		[]store.Store{blocker},
		reg,
	)

	first := testWritebackJob("aero:test:pressure:first", "first")

	if ok := q.Enqueue(first); !ok {
		t.Fatalf("first enqueue failed; want accepted")
	}

	blocker.waitUntilPutStarted(t)

	acceptedAfterFirst := 0
	dropped := false

	for i := 0; i < 100_000; i++ {
		job := testWritebackJob(
			fmt.Sprintf("aero:test:pressure:fill:%d", i),
			fmt.Sprintf("fill-%d", i),
		)

		if ok := q.Enqueue(job); !ok {
			dropped = true
			break
		}

		acceptedAfterFirst++
	}

	if !dropped {
		t.Fatalf("queue did not drop after 100000 enqueues while worker was blocked; queue may be unbounded")
	}

	if acceptedAfterFirst == 0 {
		t.Fatalf("expected at least one queued job after first blocked job")
	}

	blocker.release()

	wantWrites := 1 + acceptedAfterFirst
	waitUntilWritebackPuts(t, blocker, wantWrites, 5*time.Second)

	if got := blocker.putCount(); got != wantWrites {
		t.Fatalf("put count=%d, want %d accepted jobs", got, wantWrites)
	}
}

func testWritebackJob(storeKey string, response string) writeback.Job {
	return writeback.Job{
		Material: &key.Material{
			KeyHex:          storeKey,
			StoreKey:        storeKey,
			TokenIDs:        []uint32{1, 2, 3, 4},
			CanonicalParams: []byte(`{"model":"test-model","temperature":0}`),
			Fingerprint:     "test-fingerprint",
			Epoch:           1,
		},
		Response:    []byte(response),
		StatusCode:  200,
		ContentType: "application/json",
		TokensOut:   1,
		OriginTier:  "dev",
		TTL:         time.Minute,
	}
}

type blockingStore struct {
	name string

	mu          sync.Mutex
	data        map[store.Key]*store.Entry
	putAttempts int

	putStarted chan struct{}
	releasePut chan struct{}
	once       sync.Once
}

func newBlockingStore(name string) *blockingStore {
	return &blockingStore{
		name:       name,
		data:       make(map[store.Key]*store.Entry),
		putStarted: make(chan struct{}),
		releasePut: make(chan struct{}),
	}
}

func (s *blockingStore) Name() string {
	return s.name
}

func (s *blockingStore) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.data[key]
	if !ok {
		return nil, false, nil
	}

	cloned := cloneEntry(e)

	return cloned, true, nil
}

func (s *blockingStore) Put(ctx context.Context, key store.Key, e *store.Entry) error {
	s.mu.Lock()
	s.putAttempts++
	s.once.Do(func() {
		close(s.putStarted)
	})
	s.mu.Unlock()

	select {
	case <-s.releasePut:
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = cloneEntry(e)

	return nil
}

func (s *blockingStore) Delete(ctx context.Context, key store.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)

	return nil
}

func (s *blockingStore) waitUntilPutStarted(t *testing.T) {
	t.Helper()

	select {
	case <-s.putStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for first put to start")
	}
}

func (s *blockingStore) release() {
	close(s.releasePut)
}

func (s *blockingStore) putCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.data)
}

func waitUntilWritebackPuts(t *testing.T, s *blockingStore, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if s.putCount() == want {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %d writes; got %d", want, s.putCount())
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
