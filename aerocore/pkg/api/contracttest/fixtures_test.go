package contracttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/swaroop/aero/aerocore/pkg/api"
)

type resolveContractFixture struct {
	Request  api.PlacementRequest  `json:"request"`
	Response api.PlacementResponse `json:"response"`
}

func TestResolveContractFixtures(t *testing.T) {
	fixtures := []string{
		"resolve_route.json",
		"resolve_fail_open.json",
		"resolve_reject_tier_b.json",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "fixtures", "contracts", name)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			var fixture resolveContractFixture
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}

			if err := api.ValidatePlacementRequest(fixture.Request); err != nil {
				t.Fatalf("fixture request failed validation: %v", err)
			}

			if fixture.Request.RequestID == "" {
				t.Fatal("fixture request_id is required")
			}

			if fixture.Response.RequestID != fixture.Request.RequestID {
				t.Fatalf("response request_id must match request_id: request=%q response=%q",
					fixture.Request.RequestID,
					fixture.Response.RequestID,
				)
			}

			switch fixture.Response.Decision {
			case api.DecisionRoute:
				if fixture.Response.BackendURL == "" {
					t.Fatal("route response must include backend_url")
				}
				if fixture.Response.FailOpen {
					t.Fatal("route response must not set fail_open=true")
				}

			case api.DecisionFailOpen:
				if fixture.Response.BackendURL == "" {
					t.Fatal("fail_open response must include backend_url")
				}
				if !fixture.Response.FailOpen {
					t.Fatal("fail_open response must set fail_open=true")
				}

			case api.DecisionReject:
				if fixture.Response.BackendURL != "" {
					t.Fatal("reject response must not include backend_url")
				}
				if fixture.Response.FailOpen {
					t.Fatal("reject response must not set fail_open=true")
				}

			default:
				t.Fatalf("unknown decision: %q", fixture.Response.Decision)
			}
		})
	}
}
