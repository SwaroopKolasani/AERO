PHONY: fmt test build smoke summary chat-summary chat-proof clean

fmt:
	gofmt -w aero-rig internal

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/aerorig ./aero-rig

smoke:
	bash scripts/smoke_http.sh

summary:
	go run ./aero-rig summary-http -in out/smoke_http.jsonl

chat-summary:
	go run ./aero-rig summary-chat -in out/aerocache_chat_twice.jsonl

chat-proof:
	go run ./aero-rig proof-chat -in out/aerocache_chat_twice.jsonl -require-cache-hit -require-verified-hit -require-miss-hit

clean:
	rm -rf bin out