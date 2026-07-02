package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"aero-cache/internal/key"
	"aero-cache/internal/metrics"
)

type aeroReceipt struct {
	RequestID    string  `json:"request_id"`
	KeyPrefix    string  `json:"key_prefix,omitempty"`
	Tier         string  `json:"tier"`
	Cache        string  `json:"cache"`
	Verified     bool    `json:"verified"`
	TTFTMS       float64 `json:"ttft_ms"`
	TotalMS      float64 `json:"total_ms"`
	CostUSD      float64 `json:"cost_usd"`
	GPUSeconds   float64 `json:"gpu_seconds"`
	TokensOut    int     `json:"tokens_out"`
	AnswerSHA256 string  `json:"answer_sha256"`
	TierB        bool    `json:"tier_b"`
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "r_" + hex.EncodeToString(b[:])
	}

	return fmt.Sprintf("r_%d", time.Now().UnixNano())
}

func writeProofHeaders(w http.ResponseWriter, requestID string, verified bool) {
	w.Header().Set("X-Aero-Request-Id", requestID)
	w.Header().Set("X-Aero-Verified", strconv.FormatBool(verified))
}

func buildReceipt(
	requestID string,
	material *key.Material,
	tier string,
	cache metrics.CacheResult,
	verified bool,
	start time.Time,
	response []byte,
	tokensOut int,
	costUSD float64,
	ttft time.Duration,
) aeroReceipt {
	var keyPrefix string
	if material != nil && material.KeyHex != "" {
		keyPrefix = "blake3:" + material.KeyHex
		if len(keyPrefix) > 24 {
			keyPrefix = keyPrefix[:24] + "…"
		}
	}

	return aeroReceipt{
		RequestID:    requestID,
		KeyPrefix:    keyPrefix,
		Tier:         tier,
		Cache:        string(cache),
		Verified:     verified,
		TTFTMS:       durationMS(ttft),
		TotalMS:      durationMS(time.Since(start)),
		CostUSD:      costUSD,
		GPUSeconds:   0,
		TokensOut:    tokensOut,
		AnswerSHA256: responseSHA256(response),
		TierB:        false,
	}
}

func writeReceiptSSE(w http.ResponseWriter, receipt aeroReceipt) {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return
	}

	_, _ = w.Write([]byte("\nevent: aero-receipt\n"))
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n\n"))

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func shouldWriteReceiptSSE(wantsStream bool, contentType string) bool {
	if !wantsStream {
		return false
	}

	if contentType == "" {
		return true
	}

	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

func requestWantsStream(body []byte) bool {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return false
	}

	v, ok := obj["stream"]
	if !ok {
		return false
	}

	b, ok := v.(bool)
	return ok && b
}

func responseSHA256(response []byte) string {
	sum := sha256.Sum256(response)
	return hex.EncodeToString(sum[:])
}

func estimateCostUSD(result metrics.CacheResult, tier string, tokensOut int) float64 {
	switch result {
	case metrics.ResultHit, metrics.ResultCoalesced:
		return 0
	}

	if fixed := getenvFloat("AERO_COST_PER_MISS_USD", 0); fixed > 0 {
		return fixed
	}

	per1k := getenvFloat("AERO_COST_PER_1K_TOKENS", 0)
	if per1k <= 0 || tokensOut <= 0 {
		return 0
	}

	return (float64(tokensOut) / 1000.0) * per1k
}

func durationMS(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(d.Microseconds()) / 1000.0
}

func getenvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}

	return f
}
