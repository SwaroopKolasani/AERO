package report

import (
	"fmt"
	"strings"

	"aero-rig/internal/probe"
)

const ChatProofSchemaV1 = "aerorig.chat_proof.v1"

type ChatProofOptions struct {
	RequireCacheHit    bool
	RequireVerifiedHit bool
	RequireMissThenHit bool
}

type ProofAssertion struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type ChatSampleProof struct {
	Sample       int     `json:"sample"`
	OK           bool    `json:"ok"`
	StatusCode   int     `json:"status_code,omitempty"`
	DurationMS   float64 `json:"duration_ms"`
	AnswerSHA256 string  `json:"answer_sha256,omitempty"`
	Cache        string  `json:"cache,omitempty"`
	Tier         string  `json:"tier,omitempty"`
	Verified     string  `json:"verified,omitempty"`
	Error        string  `json:"error,omitempty"`
}

type ChatProof struct {
	SchemaVersion      string            `json:"schema_version"`
	Probe              string            `json:"probe"`
	SourceFiles        []string          `json:"source_files"`
	TotalSamples       int               `json:"total_samples"`
	OKSamples          int               `json:"ok_samples"`
	FailedSamples      int               `json:"failed_samples"`
	AnswerHashCount    int               `json:"answer_hash_count"`
	AnswerStable       bool              `json:"answer_stable"`
	CacheHitSamples    int               `json:"cache_hit_samples"`
	CacheMissSamples   int               `json:"cache_miss_samples"`
	VerifiedSamples    int               `json:"verified_samples"`
	VerifiedHitSamples int               `json:"verified_hit_samples"`
	MissThenHit        bool              `json:"miss_then_hit"`
	Passed             bool              `json:"passed"`
	Assertions         []ProofAssertion  `json:"assertions"`
	Samples            []ChatSampleProof `json:"samples"`
}

func BuildChatProof(paths []string, opts ChatProofOptions) (ChatProof, error) {
	p := ChatProof{
		SchemaVersion: ChatProofSchemaV1,
		Probe:         "chat_proof",
		SourceFiles:   paths,
		Passed:        true,
	}

	answerHashes := map[string]int{}
	seenMiss := false

	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}

		if err := readChatResults(path, func(r probe.ChatResult) {
			p.TotalSamples++

			cache := getAeroHeader(r.AeroHeaders, "X-Aero-Cache")
			tier := getAeroHeader(r.AeroHeaders, "X-Aero-Tier")
			verified := getAeroHeader(r.AeroHeaders, "X-Aero-Verified")

			sp := ChatSampleProof{
				Sample:       r.Sample,
				OK:           r.OK,
				StatusCode:   r.StatusCode,
				DurationMS:   r.DurationMS,
				AnswerSHA256: r.AnswerSHA256,
				Cache:        cache,
				Tier:         tier,
				Verified:     verified,
				Error:        r.Error,
			}
			p.Samples = append(p.Samples, sp)

			if r.OK {
				p.OKSamples++
				if strings.TrimSpace(r.AnswerSHA256) != "" {
					answerHashes[r.AnswerSHA256]++
				}
			} else {
				p.FailedSamples++
			}

			switch strings.ToLower(cache) {
			case "hit":
				p.CacheHitSamples++
				if seenMiss {
					p.MissThenHit = true
				}
			case "miss":
				p.CacheMissSamples++
				seenMiss = true
			}

			if strings.EqualFold(verified, "true") {
				p.VerifiedSamples++
				if strings.EqualFold(cache, "hit") {
					p.VerifiedHitSamples++
				}
			}
		}); err != nil {
			return p, err
		}
	}

	p.AnswerHashCount = len(answerHashes)
	p.AnswerStable = p.OKSamples > 0 && len(answerHashes) <= 1

	p.addAssertion("at_least_two_samples", p.TotalSamples >= 2, fmt.Sprintf("samples=%d", p.TotalSamples))
	p.addAssertion("no_failed_samples", p.FailedSamples == 0, fmt.Sprintf("failed=%d", p.FailedSamples))
	p.addAssertion("answer_stable", p.AnswerStable, fmt.Sprintf("answer_hash_count=%d", p.AnswerHashCount))

	if opts.RequireCacheHit {
		p.addAssertion("cache_hit_present", p.CacheHitSamples > 0, fmt.Sprintf("cache_hit_samples=%d", p.CacheHitSamples))
	}
	if opts.RequireVerifiedHit {
		p.addAssertion("verified_hit_present", p.VerifiedHitSamples > 0, fmt.Sprintf("verified_hit_samples=%d", p.VerifiedHitSamples))
	}
	if opts.RequireMissThenHit {
		p.addAssertion("miss_then_hit", p.MissThenHit, fmt.Sprintf("miss_samples=%d hit_samples=%d", p.CacheMissSamples, p.CacheHitSamples))
	}

	return p, nil
}

func (p *ChatProof) addAssertion(name string, passed bool, detail string) {
	p.Assertions = append(p.Assertions, ProofAssertion{
		Name:   name,
		Passed: passed,
		Detail: detail,
	})

	if !passed {
		p.Passed = false
	}
}
