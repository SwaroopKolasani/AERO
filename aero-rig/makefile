PHONY: fmt test build smoke summary chat-summary clean

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

clean:
	rm -rf bin out