package l3r2_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"aero-cache/internal/store"
	"aero-cache/internal/store/l3r2"
)

func TestR2SmokePutGetDelete(t *testing.T) {
	if os.Getenv("AERO_L3_ENABLED") != "1" {
		t.Skip("set AERO_L3_ENABLED=1 to run R2 smoke test")
	}

	required := []string{
		"AERO_R2_ENDPOINT",
		"AERO_R2_BUCKET",
		"AERO_R2_ACCESS_KEY_ID",
		"AERO_R2_SECRET_ACCESS_KEY",
	}

	for _, name := range required {
		if os.Getenv(name) == "" {
			t.Skipf("set %s to run R2 smoke test", name)
		}
	}

	ctx := context.Background()

	r2, err := l3r2.New(ctx, l3r2.Config{
		Enabled:         true,
		Endpoint:        os.Getenv("AERO_R2_ENDPOINT"),
		Bucket:          os.Getenv("AERO_R2_BUCKET"),
		Region:          getenvTest("AERO_R2_REGION", "auto"),
		AccessKeyID:     os.Getenv("AERO_R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AERO_R2_SECRET_ACCESS_KEY"),
		Prefix:          getenvTest("AERO_R2_PREFIX", "aerocache/test"),
		Timeout:         5 * time.Second,
		MinBytes:        0,
	})
	if err != nil {
		t.Fatalf("new r2 store: %v", err)
	}

	key := store.Key(fmt.Sprintf(
		"aero:test:r2-smoke:%d",
		time.Now().UnixNano(),
	))

	entry := &store.Entry{
		TokenIDs:    []uint32{128000, 128006, 882, 128007},
		Params:      []byte(`{"model":"test-model","temperature":0}`),
		Fingerprint: "r2-smoke-fingerprint",
		Epoch:       1,

		Response:   []byte(`{"ok":true,"source":"r2-smoke"}`),
		Compressed: false,

		CreatedAt:   time.Now().Unix(),
		TTL:         time.Hour,
		TokensOut:   1,
		OriginTier:  "dev",
		StatusCode:  200,
		ContentType: "application/json",
	}

	if err := r2.Put(ctx, key, entry); err != nil {
		t.Fatalf("r2 put: %v", err)
	}

	got, ok, err := r2.Get(ctx, key)
	if err != nil {
		t.Fatalf("r2 get: %v", err)
	}

	if !ok {
		t.Fatalf("r2 get returned ok=false after put")
	}

	if got.Fingerprint != entry.Fingerprint {
		t.Fatalf("fingerprint=%q, want %q", got.Fingerprint, entry.Fingerprint)
	}

	if got.Epoch != entry.Epoch {
		t.Fatalf("epoch=%d, want %d", got.Epoch, entry.Epoch)
	}

	if !bytes.Equal(got.Response, entry.Response) {
		t.Fatalf("response mismatch\ngot=%s\nwant=%s", string(got.Response), string(entry.Response))
	}

	if got.StatusCode != entry.StatusCode {
		t.Fatalf("status=%d, want %d", got.StatusCode, entry.StatusCode)
	}

	if got.ContentType != entry.ContentType {
		t.Fatalf("content-type=%q, want %q", got.ContentType, entry.ContentType)
	}

	if err := r2.Delete(ctx, key); err != nil {
		t.Fatalf("r2 delete: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		_, ok, err := r2.Get(ctx, key)
		if err != nil {
			t.Fatalf("r2 get after delete: %v", err)
		}

		if !ok {
			return
		}

		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("r2 object still existed after delete")
}

func getenvTest(name string, fallback string) string {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}

	return v
}
