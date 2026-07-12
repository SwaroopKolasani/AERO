package trace

import (
	"strings"
	"testing"
)

func TestNormalizeRequestID(t *testing.T) {
	got := NormalizeRequestID("  req_123  ")
	if got != "req_123" {
		t.Fatalf("expected trimmed request id, got %q", got)
	}
}

func TestNewRequestID(t *testing.T) {
	got := NewRequestID()

	if !strings.HasPrefix(got, "aerocore-") {
		t.Fatalf("expected aerocore prefix, got %q", got)
	}

	if len(got) <= len("aerocore-") {
		t.Fatalf("expected generated suffix, got %q", got)
	}
}
