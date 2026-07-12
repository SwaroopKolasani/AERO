package api

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func ValidatePlacementRequest(req PlacementRequest) error {
	if strings.TrimSpace(req.RequestID) == "" {
		return errors.New("request_id_required")
	}

	if strings.TrimSpace(req.Model) == "" {
		return errors.New("model_required")
	}

	if req.DeadlineMS < 0 {
		return errors.New("deadline_ms_must_be_non_negative")
	}

	if req.EstimatedInputTokens < 0 {
		return errors.New("estimated_input_tokens_must_be_non_negative")
	}

	if req.EstimatedOutputTokens < 0 {
		return errors.New("estimated_output_tokens_must_be_non_negative")
	}

	if req.Tier != "" && req.Tier != TierA && req.Tier != TierB {
		return errors.New("invalid_tier")
	}

	return nil
}

func ValidateBackend(b Backend) error {
	if strings.TrimSpace(b.ID) == "" {
		return errors.New("backend_id_required")
	}

	if !validRung(b.Rung) {
		return fmt.Errorf("invalid_backend_rung")
	}

	if strings.TrimSpace(b.URL) == "" {
		return errors.New("backend_url_required")
	}

	if err := validateHTTPURL(b.URL); err != nil {
		return err
	}

	if len(b.LoadedModels) == 0 && len(b.CapableModels) == 0 {
		return errors.New("backend_model_required")
	}

	if b.CostPer1KTokens < 0 {
		return errors.New("cost_per_1k_tokens_must_be_non_negative")
	}

	if b.P50LatencyMS < 0 {
		return errors.New("p50_latency_ms_must_be_non_negative")
	}

	if b.P95LatencyMS < 0 {
		return errors.New("p95_latency_ms_must_be_non_negative")
	}

	if b.MaxContext < 0 {
		return errors.New("max_context_must_be_non_negative")
	}

	return nil
}

func validRung(r Rung) bool {
	switch r {
	case RungFleet, RungGate, RungUpstream:
		return true
	default:
		return false
	}
}

func validateHTTPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("backend_url_invalid")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("backend_url_must_be_http_or_https")
	}

	if parsed.Host == "" {
		return errors.New("backend_url_host_required")
	}

	return nil
}
