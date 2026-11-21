VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: build run fetch web fmt vet test clean

build: web
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/newsd ./cmd/newsd

web:
	cd web && npm ci --no-audit --no-fund && npm run build

run:
	go run ./cmd/newsd serve

fetch:
	go run ./cmd/newsd fetch

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -rf bin web/dist
