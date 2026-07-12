package trace

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const (
	IncomingRequestIDHeader = "X-Aero-Request-Id"
	CoreRequestIDHeader     = "X-AeroCore-Request-Id"
)

func NormalizeRequestID(id string) string {
	return strings.TrimSpace(id)
}

func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "aerocore-unknown"
	}

	return "aerocore-" + hex.EncodeToString(b[:])
}
