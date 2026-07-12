package placement

import "github.com/swaroop/aero/aerocore/pkg/api"

type Rung = api.Rung

const (
	RungFleet    = api.RungFleet
	RungGate     = api.RungGate
	RungUpstream = api.RungUpstream
)

type Tier = api.Tier

const (
	TierA = api.TierA
	TierB = api.TierB
)

type Decision = api.Decision

const (
	DecisionRoute    = api.DecisionRoute
	DecisionFailOpen = api.DecisionFailOpen
	DecisionReject   = api.DecisionReject
)

type PlacementRequest = api.PlacementRequest
type PlacementResponse = api.PlacementResponse
type Backend = api.Backend
