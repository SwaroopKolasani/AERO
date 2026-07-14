.PHONY: fmt test build smoke summary chat-summary chat-proof stream-summary suite matrix clean

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

stream-summary:
	go run ./aero-rig summary-chat-stream -in out/aerocache_chat_stream_twice.jsonl

suite:
	go run ./aero-rig run-suite -manifest examples/local-suite.json

matrix:
	go run ./aero-rig build-matrix -suite-result out/suites/local/suite_result.json -out out/suites/local/matrix.json

clean:
	rm -rf bin out