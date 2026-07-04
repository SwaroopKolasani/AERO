package gate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

type Mode string

const (
	ModeStrict  Mode = "strict"
	ModeLenient Mode = "lenient"
)

type Decision struct {
	Cacheable bool   `json:"cacheable"`
	Reason    string `json:"reason"`
	Mode      Mode   `json:"mode"`
}

type Decider struct {
	mode               Mode
	tokenizerAvailable bool
}

type Config struct {
	Mode               Mode
	TokenizerAvailable bool
}

func NewDecider(cfg Config) *Decider {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeStrict
	}

	return &Decider{
		mode:               mode,
		tokenizerAvailable: cfg.TokenizerAvailable,
	}
}

func (d *Decider) Evaluate(body []byte) Decision {
	if len(bytes.TrimSpace(body)) == 0 {
		return d.bypass("empty_body")
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return d.bypass("invalid_json")
	}

	if hasToolsOrToolCalls(req) {
		return d.bypass("unsupported_tools")
	}

	if !d.tokenizerAvailable {
		return d.bypass("tokenizer_unavailable")
	}

	if !temperatureIsZero(req) {
		return d.bypass("temperature_not_zero")
	}

	if !nIsOne(req) {
		return d.bypass("n_not_one")
	}

	if !bestOfIsOneOrUnset(req) {
		return d.bypass("best_of_not_one")
	}

	return Decision{
		Cacheable: true,
		Reason:    "deterministic",
		Mode:      d.mode,
	}
}

func (d *Decider) bypass(reason string) Decision {
	return Decision{
		Cacheable: false,
		Reason:    reason,
		Mode:      d.mode,
	}
}

func temperatureIsZero(req map[string]any) bool {
	v, ok := req["temperature"]
	if !ok {
		// OpenAI-compatible default is not guaranteed deterministic.
		// Strict mode requires explicit temperature: 0.
		return false
	}

	f, ok := numberAsFloat64(v)
	if !ok {
		return false
	}

	return math.Abs(f) < 1e-12
}

func nIsOne(req map[string]any) bool {
	v, ok := req["n"]
	if !ok {
		return true
	}

	f, ok := numberAsFloat64(v)
	if !ok {
		return false
	}

	return math.Abs(f-1) < 1e-12
}

func bestOfIsOneOrUnset(req map[string]any) bool {
	v, ok := req["best_of"]
	if !ok {
		return true
	}

	f, ok := numberAsFloat64(v)
	if !ok {
		return false
	}

	return math.Abs(f) < 1e-12 || math.Abs(f-1) < 1e-12
}

func numberAsFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func (d Decision) String() string {
	if d.Cacheable {
		return fmt.Sprintf("cacheable:%s", d.Reason)
	}
	return fmt.Sprintf("bypass:%s", d.Reason)
}

func hasToolsOrToolCalls(req map[string]any) bool {
	if _, ok := req["tools"]; ok {
		return true
	}

	messagesRaw, ok := req["messages"]
	if !ok {
		return false
	}

	messages, ok := messagesRaw.([]any)
	if !ok {
		return false
	}

	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if _, ok := msg["tool_calls"]; ok {
			return true
		}

		if role, _ := msg["role"].(string); role == "tool" || role == "ipython" {
			return true
		}
	}

	return false
}

func TestStrictGateBypassesTools(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: true,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "weather?"}],
		"tools": [{"type":"function","function":{"name":"get_weather"}}],
		"temperature": 0
	}`))

	if got.Cacheable {
		t.Fatalf("expected bypass, got %#v", got)
	}

	if got.Reason != "unsupported_tools" {
		t.Fatalf("expected unsupported_tools, got %q", got.Reason)
	}
}
