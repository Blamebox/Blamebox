VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/blamebox/blamebox/internal/monitor.Version=$(VERSION)

.PHONY: build test demo check dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o blamebox ./cmd/blamebox

test:
	go vet ./...
	go test ./...

# Simulated outages, nothing is measured: http://localhost:8080
demo: build
	./blamebox -demo

# One measurement of your real connection, printed in the terminal.
check: build
	./blamebox -check

# Static binaries for the usual home servers.
dist:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/blamebox-linux-amd64 ./cmd/blamebox
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/blamebox-linux-arm64 ./cmd/blamebox
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/blamebox-linux-armv7 ./cmd/blamebox
	cd dist && sha256sum blamebox-* > SHA256SUMS

clean:
	rm -rf blamebox dist
