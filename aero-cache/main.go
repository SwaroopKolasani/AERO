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

	"aero-cache/internal/httpapi"
	"aero-cache/internal/metrics"
)

func main() {
	addr := getenv("AERO_HTTP_ADDR", ":8080")
	spaPath := getenv("AERO_SPA_PATH", "web/dist")

	reg := metrics.NewRegistry()

	handler := httpapi.NewRouter(httpapi.Config{
		SPAPath: spaPath,
		Debug:   getenv("AERO_DEBUG", "") == "1",
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