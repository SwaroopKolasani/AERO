package placement

import (
	"sort"
)

type BackendSource interface {
	ListBackends() []Backend
}

type Resolver struct {
	source BackendSource
}

func NewResolver(source BackendSource) *Resolver {
	return &Resolver{source: source}
}

func (r *Resolver) Resolve(req PlacementRequest) PlacementResponse {
	if req.Tier == TierB {
		return PlacementResponse{
			RequestID: req.RequestID,
			Decision:  DecisionReject,
			Reason:    "tier_b_not_supported_in_m3",
			FailOpen:  false,
		}
	}

	if req.Tier != "" && req.Tier != TierA {
		return PlacementResponse{
			RequestID: req.RequestID,
			Decision:  DecisionReject,
			Reason:    "unknown_tier",
			FailOpen:  false,
		}
	}

	candidates := make([]Backend, 0)

	for _, b := range r.source.ListBackends() {
		if !b.Healthy {
			continue
		}
		if !canRunModel(b, req.Model) {
			continue
		}
		if !canMeetDeadline(b, req.DeadlineMS) {
			continue
		}
		if !canFitContext(b, req.EstimatedInputTokens+req.EstimatedOutputTokens) {
			continue
		}
		candidates = append(candidates, b)
	}

	if len(candidates) == 0 {
		return PlacementResponse{
			RequestID: req.RequestID,
			Decision:  DecisionFailOpen,
			BackendID: "default-upstream",
			Rung:      RungUpstream,
			Reason:    "no_healthy_capable_backend",
			FailOpen:  true,
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]

		if rungRank(a.Rung) != rungRank(b.Rung) {
			return rungRank(a.Rung) < rungRank(b.Rung)
		}

		if a.CostPer1KTokens != b.CostPer1KTokens {
			return a.CostPer1KTokens < b.CostPer1KTokens
		}

		if a.P95LatencyMS != b.P95LatencyMS {
			return a.P95LatencyMS < b.P95LatencyMS
		}

		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}

		return a.ID < b.ID
	})

	best := candidates[0]

	return PlacementResponse{
		RequestID: req.RequestID,
		Decision:  DecisionRoute,
		BackendID: best.ID,
		Rung:      best.Rung,
		Reason:    "cheapest_healthy_capable_backend",
		FailOpen:  false,
	}
}

func canRunModel(b Backend, model string) bool {
	for _, loaded := range b.LoadedModels {
		if loaded == model {
			return true
		}
	}
	for _, capable := range b.CapableModels {
		if capable == model {
			return true
		}
	}
	return false
}

func canMeetDeadline(b Backend, deadlineMS int) bool {
	if deadlineMS <= 0 {
		return true
	}
	if b.P95LatencyMS <= 0 {
		return true
	}
	return b.P95LatencyMS <= deadlineMS
}

func canFitContext(b Backend, tokens int) bool {
	if b.MaxContext <= 0 {
		return true
	}
	return tokens <= b.MaxContext
}

func rungRank(r Rung) int {
	switch r {
	case RungFleet:
		return 0
	case RungGate:
		return 1
	case RungUpstream:
		return 2
	default:
		return 99
	}
}
