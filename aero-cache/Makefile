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



# AeroCore optional miss-placement integration.
AEROCORE_URL ?= http://localhost:8088
AEROCACHE_URL ?= http://localhost:8080
AEROCORE_TIMEOUT ?= 2s
AERO_UPSTREAM_URL ?= http://localhost:11434
AERO_SMOKE_MODEL ?= llama3.2:3b

.PHONY: aerocore-test
aerocore-test:
	AERO_L3_ENABLED=0 go test ./internal/placement -count=1
	AERO_L3_ENABLED=0 go test ./internal/httpapi -run AeroCore -count=1
	AERO_L3_ENABLED=0 AEROCORE_ENABLED=0 go test ./... -count=1

.PHONY: aerocore-smoke
aerocore-smoke:
	@echo "checking AeroCore at $(AEROCORE_URL)"
	@curl -fsS "$(AEROCORE_URL)/readyz" >/dev/null
	@echo "checking AeroCache at $(AEROCACHE_URL)"
	@curl -fsS "$(AEROCACHE_URL)/readyz" >/dev/null
	@h1=$$(mktemp); h2=$$(mktemp); b1=$$(mktemp); b2=$$(mktemp); \
	trap 'rm -f "$$h1" "$$h2" "$$b1" "$$b2"' EXIT; \
	curl -sS -D "$$h1" -o "$$b1" "$(AEROCACHE_URL)/v1/chat/completions" \
		-H 'Content-Type: application/json' \
		-d '{"model":"$(AERO_SMOKE_MODEL)","messages":[{"role":"user","content":"Say aero once."}],"temperature":0,"stream":false}'; \
	grep -qi '^X-Aero-Cache: miss' "$$h1" || { echo "expected first request to miss"; cat "$$h1"; cat "$$b1"; exit 1; }; \
	grep -qi '^X-Aero-Verified: false' "$$h1" || { echo "expected first request unverified"; cat "$$h1"; cat "$$b1"; exit 1; }; \
	curl -sS -D "$$h2" -o "$$b2" "$(AEROCACHE_URL)/v1/chat/completions" \
		-H 'Content-Type: application/json' \
		-d '{"model":"$(AERO_SMOKE_MODEL)","messages":[{"role":"user","content":"Say aero once."}],"temperature":0,"stream":false}'; \
	grep -qi '^X-Aero-Cache: hit' "$$h2" || { echo "expected second request to hit"; cat "$$h2"; cat "$$b2"; exit 1; }; \
	grep -qi '^X-Aero-Verified: true' "$$h2" || { echo "expected second request verified"; cat "$$h2"; cat "$$b2"; exit 1; }; \
	echo "AeroCore miss-placement smoke passed"
