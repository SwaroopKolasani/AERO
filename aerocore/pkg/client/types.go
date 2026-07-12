package client

type ReadyResponse struct {
	Ready                     bool   `json:"ready"`
	Reason                    string `json:"reason"`
	BackendCount              int    `json:"backend_count"`
	HealthyBackendCount       int    `json:"healthy_backend_count"`
	DefaultUpstreamConfigured bool   `json:"default_upstream_configured"`
}

type ConfigResponse struct {
	DefaultUpstreamConfigured bool `json:"default_upstream_configured"`
	BackendCount              int  `json:"backend_count"`
	HealthyBackendCount       int  `json:"healthy_backend_count"`
	StaleBackendCount         int  `json:"stale_backend_count"`
}
