package gate

import "testing"

func TestStrictGateAllowsTemperatureZero(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: true,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "say pong"}],
		"temperature": 0,
		"stream": false
	}`))

	if !got.Cacheable {
		t.Fatalf("expected cacheable, got %#v", got)
	}

	if got.Reason != "deterministic" {
		t.Fatalf("expected deterministic reason, got %q", got.Reason)
	}
}

func TestStrictGateBypassesNonZeroTemperature(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: true,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "say pong"}],
		"temperature": 0.7
	}`))

	if got.Cacheable {
		t.Fatalf("expected bypass, got %#v", got)
	}

	if got.Reason != "temperature_not_zero" {
		t.Fatalf("expected temperature_not_zero, got %q", got.Reason)
	}
}

func TestStrictGateBypassesMissingTemperature(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: true,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"messages": [{"role": "user", "content": "say pong"}]
	}`))

	if got.Cacheable {
		t.Fatalf("expected bypass, got %#v", got)
	}

	if got.Reason != "temperature_not_zero" {
		t.Fatalf("expected temperature_not_zero, got %q", got.Reason)
	}
}

func TestStrictGateBypassesNNotOne(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: true,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"n": 2
	}`))

	if got.Cacheable {
		t.Fatalf("expected bypass, got %#v", got)
	}

	if got.Reason != "n_not_one" {
		t.Fatalf("expected n_not_one, got %q", got.Reason)
	}
}

func TestStrictGateBypassesBestOfNotOne(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: true,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0,
		"best_of": 3
	}`))

	if got.Cacheable {
		t.Fatalf("expected bypass, got %#v", got)
	}

	if got.Reason != "best_of_not_one" {
		t.Fatalf("expected best_of_not_one, got %q", got.Reason)
	}
}

func TestStrictGateBypassesTokenizerUnavailable(t *testing.T) {
	d := NewDecider(Config{
		Mode:               ModeStrict,
		TokenizerAvailable: false,
	})

	got := d.Evaluate([]byte(`{
		"model": "tiny",
		"prompt": "say pong",
		"temperature": 0
	}`))

	if got.Cacheable {
		t.Fatalf("expected bypass, got %#v", got)
	}

	if got.Reason != "tokenizer_unavailable" {
		t.Fatalf("expected tokenizer_unavailable, got %q", got.Reason)
	}
}
