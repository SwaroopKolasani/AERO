package api

import "time"

type Rung string

const (
	RungFleet    Rung = "fleet"
	RungGate     Rung = "gate"
	RungUpstream Rung = "upstream"
)

type Tier string

const (
	TierA Tier = "A"
	TierB Tier = "B"
)

type Decision string

const (
	DecisionRoute    Decision = "route"
	DecisionFailOpen Decision = "fail_open"
	DecisionReject   Decision = "reject"
)

type PlacementRequest struct {
	RequestID             string `json:"request_id"`
	Model                 string `json:"model"`
	DeadlineMS            int    `json:"deadline_ms"`
	EstimatedInputTokens  int    `json:"estimated_input_tokens"`
	EstimatedOutputTokens int    `json:"estimated_output_tokens"`
	Stream                bool   `json:"stream"`
	Tier                  Tier   `json:"tier"`
}

type PlacementResponse struct {
	RequestID  string   `json:"request_id"`
	Decision   Decision `json:"decision"`
	BackendID  string   `json:"backend_id,omitempty"`
	BackendURL string   `json:"backend_url,omitempty"`
	Rung       Rung     `json:"rung,omitempty"`
	Reason     string   `json:"reason"`
	FailOpen   bool     `json:"fail_open"`
}

type Backend struct {
	ID              string    `json:"id"`
	Rung            Rung      `json:"rung"`
	URL             string    `json:"url"`
	Healthy         bool      `json:"healthy"`
	LoadedModels    []string  `json:"loaded_models"`
	CapableModels   []string  `json:"capable_models"`
	CostPer1KTokens float64   `json:"cost_per_1k_tokens"`
	P50LatencyMS    int       `json:"p50_latency_ms"`
	P95LatencyMS    int       `json:"p95_latency_ms"`
	MaxContext      int       `json:"max_context"`
	Weight          int       `json:"weight"`
	UpdatedAt       time.Time `json:"updated_at"`
}
