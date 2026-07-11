# Sith — Makefile
SHELL := /usr/bin/env bash

BINARY   := sith
PKG      := github.com/ArdurAI/sith
CMD      := ./cmd/sith
BIN_DIR  := bin
GOLANGCI ?= golangci-lint
GOVULNCHECK ?= govulncheck
KIND     ?= kind
GORELEASER ?= goreleaser

KIND_NODE_IMAGE ?= kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)

.PHONY: all build test perf e2e e2e-kind lint vuln fmt fmt-check vet tidy clean run ci release-check help

all: build

build: ## Build the sith binary into bin/
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD)

test: ## Run unit tests with the race detector and report coverage
	go test -race -count=1 -coverprofile=coverage.out ./...

perf: ## Enforce the warm-cache TUI p95 latency budget without race overhead
	go test -count=1 -run '^TestWarmViewP95UnderOneHundredMilliseconds$$' ./internal/tui

e2e: ## Build and exercise the real binary as a subprocess
	go test -race -count=1 -tags=e2e ./tests/e2e

e2e-kind: ## Exercise adapter and binary against two real kind clusters
	KIND_BIN="$(KIND)" KIND_NODE_IMAGE="$(KIND_NODE_IMAGE)" \
		go test -race -count=1 -timeout=15m -tags='e2e kind' -run '^TestKindFleetFanout$$' ./tests/e2e

lint: ## Run golangci-lint (v2)
	$(GOLANGCI) run ./...

vuln: ## Scan reachable Go call paths for known vulnerabilities
	$(GOVULNCHECK) ./...

fmt: ## Format code (gofmt + goimports via golangci-lint v2 formatters)
	$(GOLANGCI) fmt ./...

fmt-check: ## Fail if formatting/imports would change anything
	gofmt -l . | tee /dev/stderr | (! read)
	$(GOLANGCI) fmt --diff ./...

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy and verify modules
	go mod tidy
	go mod verify

clean: ## Remove build and coverage artifacts
	rm -rf $(BIN_DIR) coverage.out

run: build ## Build then run sith version
	$(BIN_DIR)/$(BINARY) version

ci: fmt-check vet lint vuln test perf e2e build ## Run the full CI gate locally

release-check: ## Build and verify the reproducible multi-platform release snapshot twice
	@command -v "$(GORELEASER)" >/dev/null || { echo "goreleaser is required" >&2; exit 1; }
	@command -v syft >/dev/null || { echo "syft is required" >&2; exit 1; }
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
		go mod download; \
		go mod verify; \
		"$(GORELEASER)" check .goreleaser.yaml; \
		"$(GORELEASER)" release --snapshot --clean --skip=sign; \
		go run ./tools/releasecheck verify --dist dist; \
		go run ./tools/releasecheck digests --dist dist > "$$tmp/first.sha256"; \
		"$(GORELEASER)" release --snapshot --clean --skip=sign; \
		go run ./tools/releasecheck verify --dist dist; \
		go run ./tools/releasecheck formula --dist dist --output dist/sith.rb; \
		go run ./tools/releasecheck digests --dist dist > "$$tmp/second.sha256"; \
		diff -u "$$tmp/first.sha256" "$$tmp/second.sha256"

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}'
