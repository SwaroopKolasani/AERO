package runtimeconfig

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultAddr            = ":8088"
	defaultReadTimeout     = 5 * time.Second
	defaultWriteTimeout    = 30 * time.Second
	defaultIdleTimeout     = 120 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

type Config struct {
	Addr               string
	DefaultUpstreamURL string
	BackendsFile       string
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	ShutdownTimeout    time.Duration
}

type GetenvFunc func(string) string

func Load(getenv GetenvFunc) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	cfg := Config{
		Addr:            getenvDefault(getenv, "AEROCORE_ADDR", defaultAddr),
		BackendsFile:    getenv("AEROCORE_BACKENDS_FILE"),
		ReadTimeout:     defaultReadTimeout,
		WriteTimeout:    defaultWriteTimeout,
		IdleTimeout:     defaultIdleTimeout,
		ShutdownTimeout: defaultShutdownTimeout,
	}

	cfg.DefaultUpstreamURL = getenv("AEROCORE_DEFAULT_UPSTREAM_URL")
	if cfg.DefaultUpstreamURL == "" {
		cfg.DefaultUpstreamURL = getenv("AERO_UPSTREAM_URL")
	}

	var err error

	cfg.ReadTimeout, err = durationFromEnv(getenv, "AEROCORE_READ_TIMEOUT", defaultReadTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.WriteTimeout, err = durationFromEnv(getenv, "AEROCORE_WRITE_TIMEOUT", defaultWriteTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.IdleTimeout, err = durationFromEnv(getenv, "AEROCORE_IDLE_TIMEOUT", defaultIdleTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.ShutdownTimeout, err = durationFromEnv(getenv, "AEROCORE_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func getenvDefault(getenv GetenvFunc, key string, fallback string) string {
	value := getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func durationFromEnv(getenv GetenvFunc, key string, fallback time.Duration) (time.Duration, error) {
	value := getenv(key)
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s invalid duration %q: %w", key, value, err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}

	return duration, nil
}
