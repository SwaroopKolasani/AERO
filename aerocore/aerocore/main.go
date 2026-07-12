package main

import (
	"log"
	"net/http"
	"os"

	"github.com/swaroop/aero/aerocore/internal/config"
	"github.com/swaroop/aero/aerocore/internal/registry"
	"github.com/swaroop/aero/aerocore/internal/server"
)

func main() {
	addr := getenvDefault("AEROCORE_ADDR", ":8088")
	defaultUpstreamURL := os.Getenv("AEROCORE_DEFAULT_UPSTREAM_URL")
	if defaultUpstreamURL == "" {
		defaultUpstreamURL = os.Getenv("AERO_UPSTREAM_URL")
	}

	reg := registry.NewMemoryRegistry()

	backendsFile := os.Getenv("AEROCORE_BACKENDS_FILE")
	if backendsFile != "" {
		backends, err := config.LoadBackends(backendsFile)
		if err != nil {
			log.Fatalf("load backends file: %v", err)
		}

		for _, b := range backends {
			reg.UpsertBackend(b)
		}

		log.Printf("aerocore loaded %d backend(s) from %s", len(backends), backendsFile)
	}

	srv := server.NewWithConfig(reg, server.Config{
		DefaultUpstreamURL: defaultUpstreamURL,
	})

	log.Printf("aerocore listening on %s", addr)
	if defaultUpstreamURL != "" {
		log.Printf("aerocore default upstream url configured")
	} else {
		log.Printf("aerocore default upstream url empty")
	}

	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatal(err)
	}
}

func getenvDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
