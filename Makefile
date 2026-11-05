VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: test build vet

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -ldflags "-X main.version=$(VERSION)" -o shh ./cmd/shh
