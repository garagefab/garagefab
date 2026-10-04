.PHONY: all build test lint vet ui ci clean

BIN_DIR := bin
BINARY := $(BIN_DIR)/garagefab

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -s -w \
	-X "github.com/garagefab/garagefab/internal/version.Version=$(VERSION)" \
	-X "github.com/garagefab/garagefab/internal/version.Commit=$(COMMIT)" \
	-X "github.com/garagefab/garagefab/internal/version.BuildDate=$(BUILD_DATE)"

all: build

ui:
	@if [ -d ui ] && [ -f ui/package.json ]; then \
		echo "Building UI..."; \
		(cd ui && npm ci --include=dev && npm run build); \
	else \
		echo "UI directory not ready yet, skipping UI build."; \
	fi

build: ui
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/garagefab

test: build
	go test -v ./...

vet:
	go vet ./...

lint: ui
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping or run via CI"

ci: build lint vet test

clean:
	rm -rf $(BIN_DIR) ui/dist
