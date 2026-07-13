package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPProbeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("aerorig\n"))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	res := HTTP(context.Background(), client, HTTPRequest{
		RunID:      "test-run",
		Sample:     1,
		TargetName: "local",
		TargetURL:  srv.URL,
		Method:     http.MethodGet,
	})

	if !res.OK {
		t.Fatalf("expected ok result, got error=%q status=%d", res.Error, res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if res.BytesRead == 0 {
		t.Fatal("expected bytes_read > 0")
	}
	if res.DurationMS < 0 {
		t.Fatal("expected non-negative duration")
	}
}

func TestHTTPProbeRejectsUnsupportedScheme(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	res := HTTP(context.Background(), client, HTTPRequest{
		TargetName: "bad",
		TargetURL:  "ftp://example.com",
		Method:     http.MethodGet,
	})

	if res.OK {
		t.Fatal("expected unsupported scheme to fail")
	}
	if !strings.Contains(res.Error, "scheme") {
		t.Fatalf("expected scheme error, got %q", res.Error)
	}
}
