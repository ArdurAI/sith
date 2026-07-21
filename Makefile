# Sith — Makefile
SHELL := /usr/bin/env bash

BINARY   := sith
PKG      := github.com/ArdurAI/sith
CMD      := ./cmd/sith
BIN_DIR  := bin
GOLANGCI ?= golangci-lint
GOVULNCHECK ?= govulncheck
PROMTOOL ?= promtool
KIND     ?= kind
HELM     ?= helm
GORELEASER ?= goreleaser
WAILS      ?= wails
WAILS_VERSION ?= v2.13.0
CODESIGN   ?= codesign
PLISTBUDDY ?= /usr/libexec/PlistBuddy
LIPO       ?= lipo
DOCKER      ?= docker
KUBECTL     ?= kubectl
OCM_SCRATCH_ROOT ?= $(shell python3 -c 'import os; print(os.path.join(os.path.realpath(os.environ.get("TMPDIR", "/tmp")), "sith-m0-{}".format(os.getuid()), "lab"))')
OCM_PREFIX       ?= sith-m0

KIND_NODE_IMAGE ?= kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5
POSTGRES_IMAGE  ?= postgres:18.4-alpine3.23@sha256:996d0920e4ff9df1fc19dacb904492f3c1ec0ec1cc338f0ad7123be7731c5f5e
ISOLATION_FUZZ_BUDGET  ?= 50000x
ISOLATION_FUZZ_WORKERS ?= 4

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)

.PHONY: all build desktop-build test test-scripts test-alert-rules perf e2e e2e-helm e2e-oci e2e-kind e2e-ocm e2e-postgres e2e-isolation lint vuln fmt fmt-check vet tidy clean run ci release-check help

all: build

build: ## Build the sith binary into bin/
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD)

desktop-build: ## Build the ad-hoc-signed macOS arm64 Sith.app development bundle
	@hack/verify-wails-version.sh "$(WAILS)" "$(WAILS_VERSION)"
	cd cmd/sith-desktop && "$(WAILS)" build -clean -m -nosyncgomod -s -trimpath -platform darwin/arm64
	@set -euo pipefail; \
		app='cmd/sith-desktop/build/bin/Sith.app'; \
		test -d "$$app"; \
		"$(LIPO)" -archs "$$app/Contents/MacOS/Sith" | grep -qx 'arm64'; \
		"$(PLISTBUDDY)" -c 'Set :CFBundleIdentifier com.ardurai.sith' "$$app/Contents/Info.plist"; \
		"$(CODESIGN)" --force --sign - "$$app"; \
		"$(CODESIGN)" --verify --strict "$$app"; \
		plutil -extract CFBundleIdentifier raw -o - "$$app/Contents/Info.plist" | grep -qx 'com.ardurai.sith'

test: ## Run unit tests with the race detector and report coverage
	go test -race -count=1 -coverprofile=coverage.out ./...

test-scripts: ## Run focused safety tests for operator-facing shell harnesses
	bash tests/scripts/wails_tooling_policy_test.sh
	bash tests/scripts/release_tooling_policy_test.sh
	bash tests/scripts/helm_tooling_policy_test.sh
	bash tests/scripts/m0_ocm_falsification_safety_test.sh
	bash tests/scripts/release_tag_identity_guide_test.sh
	bash tests/scripts/release_tag_policy_test.sh
	bash tests/scripts/release_pr_gate_policy_test.sh
	bash tests/scripts/release_hub_image_policy_test.sh
	bash tests/scripts/prometheus_tooling_policy_test.sh

test-alert-rules: ## Validate and unit-test the portable Prometheus alert contract
	@promtool_path="$$(command -v "$(PROMTOOL)")" || { echo "promtool is required" >&2; exit 1; }; \
	case "$$promtool_path" in /*) ;; *) promtool_path="$$(pwd)/$$promtool_path" ;; esac; \
	cd monitoring && "$$promtool_path" check rules sith-hub.rules.yml && \
	"$$promtool_path" test rules sith-hub.rules.test.yml

perf: ## Enforce the warm-cache TUI p95 latency budget without race overhead
	go test -count=1 -run '^TestWarmViewP95UnderOneHundredMilliseconds$$' ./internal/tui

e2e: ## Build and exercise the real binary as a subprocess
	go test -race -count=1 -tags=e2e ./tests/e2e

e2e-helm: ## Validate the fail-closed Helm hub chart with the pinned Helm CLI
	HELM_BIN="$(HELM)" go test -race -count=1 -timeout=5m -tags='e2e helm' -run '^TestHelmHubChartContract$$' ./tests/e2e

e2e-oci: ## Build and inspect the local immutable OCI image contract for linux/amd64 and linux/arm64
	go test -race -count=1 -timeout=10m -tags='e2e oci' -run '^Test(OCIImageCrossPlatformContract|ContainerfileInstructionGuard)$$' ./tests/e2e

e2e-kind: ## Exercise adapter and binary against two real kind clusters
	KIND_BIN="$(KIND)" KIND_NODE_IMAGE="$(KIND_NODE_IMAGE)" \
		go test -race -count=1 -timeout=15m -tags='e2e kind' -run '^Test(KindFleetFanout|KindOCIImageContract|KindArgoApplicationProjection)$$' ./tests/e2e

e2e-ocm: ## Prove direct ClusterProxy transport in the pinned two-spoke M0 lab
	@set -euo pipefail; \
		run_required_e2e_test() { \
			local test_name="$$1"; shift; \
			local output; \
			if ! output="$$("$$@" 2>&1)"; then \
				printf '%s\n' "$$output" >&2; return 1; \
			fi; \
			printf '%s\n' "$$output"; \
			grep -Fq -- "--- PASS: $${test_name}" <<<"$$output" || { \
				echo "required M0 test $${test_name} did not run" >&2; return 1; \
			}; \
		}; \
		trap 'KIND_BIN="$(KIND)" SITH_M0_SCRATCH_ROOT="$(OCM_SCRATCH_ROOT)" SITH_M0_PREFIX="$(OCM_PREFIX)" hack/experiments/m0-ocm-falsification.sh cleanup' EXIT; \
		KIND_BIN="$(KIND)" SITH_M0_SCRATCH_ROOT="$(OCM_SCRATCH_ROOT)" SITH_M0_PREFIX="$(OCM_PREFIX)" SITH_M0_KEEP_CLUSTERS=1 \
			hack/experiments/m0-ocm-falsification.sh run; \
		export KUBECTL_BIN="$(KUBECTL)" SITH_OCM_HUB_KUBECONFIG="$(OCM_SCRATCH_ROOT)/kubeconfig" SITH_OCM_HUB_CONTEXT="kind-$(OCM_PREFIX)-hub"; \
		run_required_e2e_test TestDirectClusterProxyM0 \
			go test -v -race -count=1 -timeout=8m -tags='e2e ocm' -run '^TestDirectClusterProxyM0$$' ./internal/hubocm; \
		run_required_e2e_test TestHubRuntimeDirectClusterProxyM0 \
			go test -v -race -count=1 -timeout=8m -tags='e2e ocm' -run '^TestHubRuntimeDirectClusterProxyM0$$' ./internal/hubruntime

e2e-postgres: ## Prove forced RLS against a temporary digest-pinned PostgreSQL container
	DOCKER_BIN="$(DOCKER)" POSTGRES_IMAGE="$(POSTGRES_IMAGE)" \
		go test -race -count=1 -cover -timeout=5m -tags=postgres -run '^TestPostgresRLSBackstop$$' ./internal/hubdb

e2e-isolation: ## Run signed-identity, scoped-query, and real PostgreSQL isolation invariants together
	DOCKER_BIN="$(DOCKER)" POSTGRES_IMAGE="$(POSTGRES_IMAGE)" \
		go test -race -count=1 -cover -timeout=5m -tags=postgres \
			./internal/hubauth ./internal/hubserver ./internal/fleetcache ./internal/hubdb
	go test -run '^$$' -fuzz '^FuzzQueryScopedNeverLeaksForeignWorkspace$$' \
		-fuzztime="$(ISOLATION_FUZZ_BUDGET)" -parallel="$(ISOLATION_FUZZ_WORKERS)" \
		-timeout=2m ./internal/fleetcache
	go test -run '^$$' -fuzz '^FuzzStoreForeignWorkspaceMutationCannotChangeEitherWorkspace$$' \
		-fuzztime="$(ISOLATION_FUZZ_BUDGET)" -parallel="$(ISOLATION_FUZZ_WORKERS)" \
		-timeout=2m ./internal/fleetcache

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

ci: fmt-check vet lint vuln test test-scripts test-alert-rules perf e2e build ## Run the full CI gate locally

release-check: ## Build, verify, and package the reproducible multi-platform release snapshot twice
	@command -v "$(GORELEASER)" >/dev/null || { echo "goreleaser is required" >&2; exit 1; }
	@command -v syft >/dev/null || { echo "syft is required" >&2; exit 1; }
	@set -e; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
		go mod download; \
		go mod verify; \
		"$(GORELEASER)" check .goreleaser.yaml; \
		"$(GORELEASER)" release --snapshot --clean --skip=sign; \
		go run ./tools/releasecheck verify --dist dist; \
		go run ./tools/releasecheck digests --dist dist > "$$tmp/first.sha256"; \
		"$(GORELEASER)" release --snapshot --clean --skip=sign; \
		go run ./tools/releasecheck verify --dist dist; \
		go run ./tools/releasecheck formula --dist dist --output dist/sith.rb; \
		DOCKER_BIN="$(DOCKER)" hack/verify-release-hub-image.sh --dist dist; \
		go run ./tools/releasecheck digests --dist dist > "$$tmp/second.sha256"; \
		diff -u "$$tmp/first.sha256" "$$tmp/second.sha256"

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}'
