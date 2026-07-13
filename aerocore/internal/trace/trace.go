package trace

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/swaroop/aero/aerocore/pkg/api"
)

const (
	IncomingRequestIDHeader = api.IncomingRequestIDHeader
	CoreRequestIDHeader     = api.CoreRequestIDHeader
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
