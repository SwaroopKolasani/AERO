//Wire up the registry, boot the HTTP server

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aero-cache/internal/gate"
	"aero-cache/internal/httpapi"
	"aero-cache/internal/key"
	"aero-cache/internal/metrics"
	"strconv"
)

func main() {
	addr := getenv("AERO_HTTP_ADDR", ":8080")
	spaPath := getenv("AERO_SPA_PATH", "web/dist")

	reg := metrics.NewRegistry()

	handler := httpapi.NewRouter(httpapi.Config{
		SPAPath:            spaPath,
		Debug:              getenv("AERO_DEBUG", "") == "1",
		GateMode:           gate.Mode(getenv("AERO_GATE_MODE", "strict")),
		TokenizerAvailable: getenv("AERO_TOKENIZER_AVAILABLE", "1") == "1",
		Epoch:              getenvUint64("AERO_EPOCH", 0),
		Fingerprint: key.Fingerprint{
			Model:  getenv("AERO_FINGERPRINT_MODEL", "dev/tiny@local"),
			Engine: getenv("AERO_FINGERPRINT_ENGINE", "ollama@local"),
			Config: map[string]any{
				"dtype": getenv("AERO_FINGERPRINT_DTYPE", "cpu"),
				"tp":    1,
			},
		},
	}, reg)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // Keep streaming endpoints unrestricted.
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)

	go func() {
		log.Printf("aerocache listening on %s", addr)
		errCh <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-stop:
		log.Printf("shutdown signal received: %s", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Fatalf("graceful shutdown failed: %v", err)
		}

	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}
}

func getenv(key string, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func getenvUint64(keyName string, fallback uint64) uint64 {
	v := os.Getenv(keyName)
	if v == "" {
		return fallback
	}

	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return fallback
	}

	return n
}
