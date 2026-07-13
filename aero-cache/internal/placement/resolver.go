package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DecisionRoute    = "route"
	DecisionFailOpen = "fail_open"

	SourceDefault          = "default"
	SourceAeroCoreRoute    = "aerocore_route"
	SourceAeroCoreFailOpen = "aerocore_fail_open"
	SourceFallback         = "fallback"
)

type Config struct {
	Enabled     bool
	BaseURL     string
	Timeout     time.Duration
	FallbackURL string
}

type Resolver struct {
	enabled     bool
	baseURL     string
	fallbackURL string
	httpClient  *http.Client
}

type Request struct {
	RequestID             string
	Model                 string
	DeadlineMS            int
	EstimatedInputTokens  int
	EstimatedOutputTokens int
	Stream                bool
	Tier                  string
}

type Target struct {
	BaseURL   string
	Source    string
	Decision  string
	BackendID string
	Rung      string
	Reason    string
	FailOpen  bool
}

type coreRequest struct {
	RequestID             string `json:"request_id"`
	Model                 string `json:"model"`
	DeadlineMS            int    `json:"deadline_ms"`
	EstimatedInputTokens  int    `json:"estimated_input_tokens"`
	EstimatedOutputTokens int    `json:"estimated_output_tokens"`
	Stream                bool   `json:"stream"`
	Tier                  string `json:"tier"`
}

type coreResponse struct {
	RequestID  string `json:"request_id"`
	Decision   string `json:"decision"`
	BackendID  string `json:"backend_id,omitempty"`
	BackendURL string `json:"backend_url,omitempty"`
	Rung       string `json:"rung,omitempty"`
	Reason     string `json:"reason"`
	FailOpen   bool   `json:"fail_open"`
}

func NewResolver(cfg Config) *Resolver {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	return &Resolver{
		enabled:     cfg.Enabled,
		baseURL:     strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		fallbackURL: strings.TrimRight(strings.TrimSpace(cfg.FallbackURL), "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (r *Resolver) Resolve(ctx context.Context, req Request) Target {
	if r == nil {
		return Target{Source: SourceDefault}
	}

	if !r.enabled {
		return Target{
			BaseURL: r.fallbackURL,
			Source:  SourceDefault,
		}
	}

	if r.baseURL == "" {
		return r.fallback("aerocore_url_empty")
	}

	if strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.Model) == "" {
		return r.fallback("invalid_placement_request")
	}

	payload := coreRequest{
		RequestID:             strings.TrimSpace(req.RequestID),
		Model:                 strings.TrimSpace(req.Model),
		DeadlineMS:            req.DeadlineMS,
		EstimatedInputTokens:  req.EstimatedInputTokens,
		EstimatedOutputTokens: req.EstimatedOutputTokens,
		Stream:                req.Stream,
		Tier:                  strings.TrimSpace(req.Tier),
	}
	if payload.Tier == "" {
		payload.Tier = "A"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return r.fallback("aerocore_marshal_failed")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/resolve", bytes.NewReader(data))
	if err != nil {
		return r.fallback("aerocore_request_build_failed")
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Aero-Request-Id", payload.RequestID)

	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return r.fallback("aerocore_unavailable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return r.fallback("aerocore_non_200")
	}

	var out coreResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return r.fallback("aerocore_decode_failed")
	}

	targetURL := strings.TrimSpace(out.BackendURL)
	switch out.Decision {
	case DecisionRoute:
		if !validTargetURL(targetURL) {
			return r.fallback("aerocore_route_missing_backend_url")
		}
		return Target{
			BaseURL:   strings.TrimRight(targetURL, "/"),
			Source:    SourceAeroCoreRoute,
			Decision:  out.Decision,
			BackendID: out.BackendID,
			Rung:      out.Rung,
			Reason:    out.Reason,
			FailOpen:  out.FailOpen,
		}

	case DecisionFailOpen:
		if !validTargetURL(targetURL) {
			return r.fallback("aerocore_fail_open_missing_backend_url")
		}
		return Target{
			BaseURL:   strings.TrimRight(targetURL, "/"),
			Source:    SourceAeroCoreFailOpen,
			Decision:  out.Decision,
			BackendID: out.BackendID,
			Rung:      out.Rung,
			Reason:    out.Reason,
			FailOpen:  out.FailOpen,
		}

	default:
		return r.fallback("aerocore_no_usable_decision")
	}
}

func (r *Resolver) fallback(reason string) Target {
	return Target{
		BaseURL:  r.fallbackURL,
		Source:   SourceFallback,
		Decision: DecisionFailOpen,
		Rung:     "upstream",
		Reason:   reason,
		FailOpen: true,
	}
}

func validTargetURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}