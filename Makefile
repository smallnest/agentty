# agentty — development and release tasks.
#
# Run `make` or `make help` for the target list.

BINARY   := agentty
GO       ?= go
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
GOFLAGS  := -trimpath

.DEFAULT_GOAL := help

.PHONY: help build install run test test-race cover vet fmt fmt-check lint tidy check script-check clean snapshot release tools

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary into bin/
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/agentty

install: ## Install the binary into GOBIN
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/agentty

run: ## Run the daemon from source
	$(GO) run ./cmd/agentty

test: ## Run the test suite
	$(GO) test ./...

test-race: ## Run the test suite with the race detector
	$(GO) test -race -count=1 ./...

cover: ## Write coverage.out and coverage.html
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format Go sources
	gofmt -w .

fmt-check: ## Fail when sources need formatting
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

lint: ## Run golangci-lint when available, otherwise go vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || $(GO) vet ./...

tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

check: fmt-check vet script-check test ## Format check, vet, script check, and tests

script-check: ## Check the installer syntax
	sh -n scripts/install.sh

clean: ## Remove build artifacts
	rm -rf bin dist coverage.out coverage.html

snapshot: ## Build a local GoReleaser snapshot into dist/
	goreleaser release --snapshot --clean

release: ## Publish a release from the current tag
	goreleaser release --clean

tools: ## Install GoReleaser
	$(GO) install github.com/goreleaser/goreleaser/v2@latest
