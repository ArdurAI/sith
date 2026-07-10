# Sith — Makefile
SHELL := /usr/bin/env bash

BINARY   := sith
PKG      := github.com/ArdurAI/sith
CMD      := ./cmd/sith
BIN_DIR  := bin
GOLANGCI ?= golangci-lint
GOVULNCHECK ?= govulncheck
KIND     ?= kind

KIND_NODE_IMAGE ?= kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)

.PHONY: all build test e2e e2e-kind lint vuln fmt fmt-check vet tidy clean run ci help

all: build

build: ## Build the sith binary into bin/
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD)

test: ## Run unit tests with the race detector and report coverage
	go test -race -count=1 -coverprofile=coverage.out ./...

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

ci: fmt-check vet lint vuln test e2e build ## Run the full CI gate locally

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}'
