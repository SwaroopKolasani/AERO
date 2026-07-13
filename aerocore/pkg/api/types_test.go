package api

import (
	"encoding/json"
	"testing"
)

func TestPlacementResponseJSONContract(t *testing.T) {
	resp := PlacementResponse{
		RequestID:  "req_contract",
		Decision:   DecisionRoute,
		BackendID:  "mac-m2-ollama",
		BackendURL: "http://mac.local:11434",
		Rung:       RungFleet,
		Reason:     "cheapest_healthy_capable_backend",
		FailOpen:   false,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got["request_id"] != "req_contract" {
		t.Fatalf("missing request_id contract: %s", string(data))
	}

	if got["backend_url"] != "http://mac.local:11434" {
		t.Fatalf("missing backend_url contract: %s", string(data))
	}

	if got["decision"] != "route" {
		t.Fatalf("missing decision contract: %s", string(data))
	}
}

func TestHeaderConstants(t *testing.T) {
	if IncomingRequestIDHeader != "X-Aero-Request-Id" {
		t.Fatalf("unexpected incoming request id header: %q", IncomingRequestIDHeader)
	}

	if CoreRequestIDHeader != "X-AeroCore-Request-Id" {
		t.Fatalf("unexpected core request id header: %q", CoreRequestIDHeader)
	}
}
