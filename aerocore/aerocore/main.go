package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/swaroop/aero/aerocore/internal/config"
	"github.com/swaroop/aero/aerocore/internal/registry"
	"github.com/swaroop/aero/aerocore/internal/runtimeconfig"
	"github.com/swaroop/aero/aerocore/internal/server"
)

func main() {
	cfg, err := runtimeconfig.Load(os.Getenv)
	if err != nil {
		log.Fatalf("load runtime config: %v", err)
	}

	reg := registry.NewMemoryRegistry()

	if cfg.BackendsFile != "" {
		backends, err := config.LoadBackends(cfg.BackendsFile)
		if err != nil {
			log.Fatalf("load backends file: %v", err)
		}

		for _, b := range backends {
			reg.UpsertBackend(b)
		}

		log.Printf("aerocore loaded %d backend(s) from %s", len(backends), cfg.BackendsFile)
	}

	handler := server.NewWithConfig(reg, server.Config{
		DefaultUpstreamURL: cfg.DefaultUpstreamURL,
	})

	httpServer := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	errCh := make(chan error, 1)

	go func() {
		log.Printf("aerocore listening on %s", cfg.Addr)
		log.Printf("aerocore http timeouts read=%s write=%s idle=%s shutdown=%s",
			cfg.ReadTimeout,
			cfg.WriteTimeout,
			cfg.IdleTimeout,
			cfg.ShutdownTimeout,
		)

		if cfg.DefaultUpstreamURL != "" {
			log.Printf("aerocore default upstream url configured")
		} else {
			log.Printf("aerocore default upstream url empty")
		}

		errCh <- httpServer.ListenAndServe()
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-signalCh:
		log.Printf("aerocore shutdown signal=%s", sig)

		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := httpServer.Shutdown(ctx); err != nil {
			log.Fatalf("aerocore graceful shutdown failed: %v", err)
		}

		log.Printf("aerocore shutdown complete")

	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("aerocore server failed: %v", err)
		}
	}
}
