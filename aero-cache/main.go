// Wire up the registry and boot the HTTP server.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"aero-cache/internal/gate"
	"aero-cache/internal/httpapi"
	"aero-cache/internal/key"
	"aero-cache/internal/metrics"
)

func main() {
	addr := getenv("AERO_HTTP_ADDR", ":8080")
	spaPath := getenv("AERO_SPA_PATH", "web/dist")

	reg := metrics.NewRegistry()

	chatTemplateDate := getenv("AERO_CHAT_TEMPLATE_DATE", "02 Jul 2026")

	tokenizerAvailable := false
	tokenizer := key.Tokenizer(key.ByteTokenizer{})
	renderer := key.Renderer(key.LegacyRenderer{})

	fpConfig := map[string]any{
		"dtype":              getenv("AERO_FINGERPRINT_DTYPE", "cpu"),
		"tp":                 1,
		"chat_template_date": chatTemplateDate,
	}

	if dir := getenv("AERO_TOKENIZER_DIR", ""); dir != "" {
		bundle, err := key.LoadTokenizerBundle(key.TokenizerBundleConfig{
			Dir:              dir,
			ChatTemplateKind: getenv("AERO_CHAT_TEMPLATE_KIND", ""),
			ChatTemplateDate: chatTemplateDate,
		})
		if err != nil {
			log.Printf("aerocache: tokenizer unavailable; cache will bypass: %v", err)
		} else {
			tokenizerAvailable = true
			tokenizer = bundle.Tokenizer
			renderer = bundle.Renderer

			fpConfig["tokenizer_sha256"] = bundle.TokenizerSHA256
			fpConfig["tokenizer_config_sha256"] = bundle.TokenizerConfigSHA256
			fpConfig["chat_template_sha256"] = bundle.ChatTemplateSHA256
			fpConfig["chat_template_kind"] = bundle.ChatTemplateKind
			fpConfig["chat_template_date"] = bundle.ChatTemplateDate
		}
	} else if getenv("AERO_ALLOW_BYTE_TOKENIZER", "") == "1" {
		log.Printf("aerocache: using ByteTokenizer because AERO_ALLOW_BYTE_TOKENIZER=1")

		tokenizerAvailable = true

		fpConfig["tokenizer"] = "byte-tokenizer-dev-only"
		fpConfig["chat_template_kind"] = "legacy-dev-only"
		fpConfig["chat_template_date"] = chatTemplateDate
	}

	handler := httpapi.NewRouter(httpapi.Config{
		SPAPath:            spaPath,
		Debug:              getenv("AERO_DEBUG", "") == "1",
		GateMode:           gate.Mode(getenv("AERO_GATE_MODE", "strict")),
		TokenizerAvailable: tokenizerAvailable,
		Epoch:              getenvUint64("AERO_EPOCH", 0),
		UpstreamURL:        getenv("AERO_UPSTREAM_URL", "http://localhost:11434"),
		AeroCoreEnabled:    getenvBool("AEROCORE_ENABLED", false),
		AeroCoreURL:        getenv("AEROCORE_URL", ""),
		AeroCoreTimeout:    getenvDuration("AEROCORE_TIMEOUT", 2*time.Second),
		Tokenizer:          tokenizer,
		Renderer:           renderer,
		Fingerprint: key.Fingerprint{
			Model:  getenv("AERO_FINGERPRINT_MODEL", "llama3.2:3b"),
			Engine: getenv("AERO_FINGERPRINT_ENGINE", "ollama@local"),
			Config: fpConfig,
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

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}

	return parsed
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}

	return parsed
}
