.PHONY: fmt test smoke run build clean

fmt:
	gofmt -w aerocore internal pkg

test:
	go test ./...

smoke:
	./scripts/smoke_core.sh

run:
	AEROCORE_DEFAULT_UPSTREAM_URL=http://localhost:11434 \
	AEROCORE_BACKENDS_FILE=configs/backends.example.json \
	go run ./aerocore

build:
	mkdir -p bin
	go build -o bin/aerocore ./aerocore

clean:
	rm -rf bin