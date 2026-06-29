package lookup

import (
	"context"
	"time"

	"aero-cache/internal/key"
	"aero-cache/internal/store"
	"aero-cache/internal/verify"
)

type Metrics interface {
	IncVerifyMismatch()
}

type Result struct {
	Hit          bool
	Tier         string
	Entry        *store.Entry
	VerifyReason string
	LookupTrace  []TierResult
}

type TierResult struct {
	Tier        string        `json:"tier"`
	Hit         bool          `json:"hit"`
	Verified    bool          `json:"verified"`
	Err         string        `json:"err,omitempty"`
	Duration    time.Duration `json:"duration"`
	PromotedTo  []string      `json:"promoted_to,omitempty"`
	VerifyCause string        `json:"verify_cause,omitempty"`
}

type Tier struct {
	Store  store.Store
	Budget time.Duration
}

type Orchestrator struct {
	tiers   []Tier
	metrics Metrics
}

func New(tiers []Tier, metrics Metrics) *Orchestrator {
	filtered := make([]Tier, 0, len(tiers))
	for _, t := range tiers {
		if t.Store != nil {
			filtered = append(filtered, t)
		}
	}

	return &Orchestrator{
		tiers:   filtered,
		metrics: metrics,
	}
}

func (o *Orchestrator) Lookup(ctx context.Context, material *key.Material) Result {
	trace := make([]TierResult, 0, len(o.tiers))

	if material == nil {
		return Result{
			Hit:          false,
			VerifyReason: "nil_material",
			LookupTrace:  trace,
		}
	}

	cacheKey := store.Key(material.StoreKey)

	for i, tier := range o.tiers {
		tierName := tier.Store.Name()
		start := time.Now()

		tierCtx := ctx
		cancel := func() {}

		if tier.Budget > 0 {
			tierCtx, cancel = context.WithTimeout(ctx, tier.Budget)
		}

		entry, found, err := tier.Store.Get(tierCtx, cacheKey)
		cancel()

		tr := TierResult{
			Tier:     tierName,
			Duration: time.Since(start),
		}

		if err != nil {
			tr.Err = err.Error()
			trace = append(trace, tr)
			continue
		}

		if !found {
			trace = append(trace, tr)
			continue
		}

		tr.Hit = true

		vr := verify.Entry(material, entry)
		if !vr.OK {
			tr.VerifyCause = vr.Reason

			if o.metrics != nil {
				o.metrics.IncVerifyMismatch()
			}

			_ = tier.Store.Delete(ctx, cacheKey)

			trace = append(trace, tr)
			continue
		}

		tr.Verified = true

		promoted := o.promote(ctx, cacheKey, entry, i)
		tr.PromotedTo = promoted

		trace = append(trace, tr)

		return Result{
			Hit:          true,
			Tier:         "cache-" + tierName,
			Entry:        entry,
			VerifyReason: "verified",
			LookupTrace:  trace,
		}
	}

	return Result{
		Hit:          false,
		VerifyReason: "miss",
		LookupTrace:  trace,
	}
}

func (o *Orchestrator) promote(ctx context.Context, key store.Key, entry *store.Entry, hitIndex int) []string {
	if hitIndex <= 0 {
		return nil
	}

	promoted := []string{}

	for i := 0; i < hitIndex; i++ {
		t := o.tiers[i]
		if t.Store == nil {
			continue
		}

		if err := t.Store.Put(ctx, key, entry); err == nil {
			promoted = append(promoted, t.Store.Name())
		}
	}

	return promoted
}
