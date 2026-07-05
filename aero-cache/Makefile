SHELL := /bin/bash

AERO_HTTP_ADDR ?= :8080
AERO_SPA_PATH=../aero-bench/web/dist 
AERO_UPSTREAM_URL ?= http://localhost:11434
AERO_TOKENIZER_DIR ?= ./model-tokenizer
AERO_CHAT_TEMPLATE_KIND ?= llama3
AERO_CHAT_TEMPLATE_DATE ?= 04 Jul 2026
AERO_FINGERPRINT_MODEL ?= llama3.2:3b
AERO_FINGERPRINT_ENGINE ?= ollama@local
AERO_L2_ADDR ?= 127.0.0.1:6379

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: run
run:
	AERO_HTTP_ADDR="$(AERO_HTTP_ADDR)" \
	AERO_SPA_PATH=../aero-bench/web/dist \
	AERO_UPSTREAM_URL="$(AERO_UPSTREAM_URL)" \
	AERO_TOKENIZER_DIR="$(AERO_TOKENIZER_DIR)" \
	AERO_CHAT_TEMPLATE_KIND="$(AERO_CHAT_TEMPLATE_KIND)" \
	AERO_CHAT_TEMPLATE_DATE="$(AERO_CHAT_TEMPLATE_DATE)" \
	AERO_FINGERPRINT_MODEL="$(AERO_FINGERPRINT_MODEL)" \
	AERO_FINGERPRINT_ENGINE="$(AERO_FINGERPRINT_ENGINE)" \
	go run .

.PHONY: run-l2
run-l2:
	AERO_HTTP_ADDR="$(AERO_HTTP_ADDR)" \
	AERO_SPA_PATH=../aero-bench/web/dist \
	AERO_UPSTREAM_URL="$(AERO_UPSTREAM_URL)" \
	AERO_TOKENIZER_DIR="$(AERO_TOKENIZER_DIR)" \
	AERO_CHAT_TEMPLATE_KIND="$(AERO_CHAT_TEMPLATE_KIND)" \
	AERO_CHAT_TEMPLATE_DATE="$(AERO_CHAT_TEMPLATE_DATE)" \
	AERO_FINGERPRINT_MODEL="$(AERO_FINGERPRINT_MODEL)" \
	AERO_FINGERPRINT_ENGINE="$(AERO_FINGERPRINT_ENGINE)" \
	AERO_L2_ADDR="$(AERO_L2_ADDR)" \
	go run .

.PHONY: run-dev-byte
run-dev-byte:
	AERO_HTTP_ADDR="$(AERO_HTTP_ADDR)" \
	AERO_SPA_PATH=../aero-bench/web/dist \
	AERO_UPSTREAM_URL="$(AERO_UPSTREAM_URL)" \
	AERO_ALLOW_BYTE_TOKENIZER=1 \
	AERO_FINGERPRINT_MODEL="$(AERO_FINGERPRINT_MODEL)" \
	AERO_FINGERPRINT_ENGINE="$(AERO_FINGERPRINT_ENGINE)" \
	go run .

.PHONY: valkey
valkey:
	docker compose up valkey

.PHONY: valkey-detach
valkey-detach:
	docker compose up -d valkey

.PHONY: valkey-down
valkey-down:
	docker compose down

.PHONY: parity
parity:
	AERO_TOKENIZER_DIR="$(AERO_TOKENIZER_DIR)" \
	AERO_SPA_PATH=../aero-bench/web/dist \
	AERO_CHAT_TEMPLATE_KIND="$(AERO_CHAT_TEMPLATE_KIND)" \
	AERO_CHAT_TEMPLATE_DATE="$(AERO_CHAT_TEMPLATE_DATE)" \
	AERO_PARITY_CASE_GLOB="$$(pwd)/tools/tokenizer_parity/cases/*.json" \
	go test ./internal/key -run TestLlama3TokenizerParityAgainstHFReference -v

.PHONY: l2-test
l2-test:
	AERO_L2_ADDR="$(AERO_L2_ADDR)" \
	go test ./internal/httpapi -run TestIntegrationL2PersistenceAcrossRouterRestart -v

.PHONY: integration
integration:
	go test ./internal/httpapi -run TestIntegration -v
	go test ./internal/lookup -run 'TestLookupRejectsAndDeletes' -v
	go test ./internal/writeback -run TestWritebackQueueDropsWhenFull -v

.PHONY: load
load:
	python tools/load_hotkey.py \
		--url http://127.0.0.1:8080/v1/chat/completions \
		--model "$(AERO_FINGERPRINT_MODEL)" \
		--requests 200 \
		--concurrency 20 \
		--prompt "Say exactly pong."

.PHONY: smoke
smoke: test parity integration

.PHONY: clean-cache
clean-cache:
	redis-cli -h 127.0.0.1 -p 6379 FLUSHDB