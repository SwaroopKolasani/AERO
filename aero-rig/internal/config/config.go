package config

import (
	"os"
	"time"
)

func DefaultTimeout() time.Duration {
	raw := os.Getenv("AERORIG_TIMEOUT")
	if raw == "" {
		return 2 * time.Second
	}

	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 2 * time.Second
	}

	return d
}

func DefaultOutputPath() string {
	return os.Getenv("AERORIG_OUTPUT")
}
