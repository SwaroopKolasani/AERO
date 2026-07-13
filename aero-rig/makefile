.PHONY: fmt test build smoke clean

fmt:
	gofmt -w aero-rig internal

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/aerorig ./aero-rig

smoke:
	bash scripts/smoke_http.sh

clean:
	rm -rf bin out