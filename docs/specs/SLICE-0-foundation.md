# Spec — Slice 0: Foundation Walking-Skeleton

**Status:** ready to build · **Date:** 2026-07-10 · **Target builder:** a fresh Sonnet session, max effort
**Branch:** `feat/slice-0-foundation` off `dev` · **PR target:** `dev`
**Prereqs to read:** [`../CONVENTIONS.md`](../CONVENTIONS.md) (commits, CI gates, style),
[`../BUILD-SEQUENCE.md`](../BUILD-SEQUENCE.md) (where this slice sits),
[`../adr/0002-stack-and-language.md`](../adr/0002-stack-and-language.md) (Go, single binary).

This spec is **self-contained**: build exactly what is written here, nothing more. Do not implement
kubeconfig fan-out, real clusters, TUI, web UI, MCP, or keychain — those are Slices 1–6. Slice 0 is
the scaffold plus one end-to-end path through a **stubbed** fleet source. All disk work stays on
`/Volumes/EXTENDED` (never the system disk).

---

## 0. Definition of done (read this first)

A reviewer merges this PR when **all** of the following are true:

1. `make build` produces `bin/sith` with version metadata injected via ldflags.
2. `bin/sith version` prints build info (text) and `bin/sith version -o json` prints valid JSON.
3. `bin/sith clusters` calls a **stubbed `fleet.Source`**, receives an **empty typed `FleetResult`**,
   and prints a clean "no clusters" result; `-o json` prints `{"clusters":[],"coverage":{…}}`. Exit 0.
4. `bin/sith ui` and `bin/sith hub` print a clear "not yet implemented — see <issue>" line, exit 0.
5. `bin/sith` (no args) and `bin/sith --help` print usage, exit 0. An unknown subcommand exits non-zero.
6. `make ci` is green locally: `fmt-check`, `vet`, `lint`, `test -race`, `build`.
7. GitHub Actions CI is green on the PR (same gates).
8. Every package in §3 has the tests listed in §7, and they pass under `-race`.
9. `sessions/` already has `README.md` + `JOURNAL-TEMPLATE.md` (from the plan branch); this slice
   **adds** `sessions/2026-07-<dd>-slice-0-foundation.md` with a `[C]` checkpoint per commit and
   matching `GSTACK-Checkpoint` trailers.
10. All commits are Conventional + DCO signed-off + SSH-signed, **no AI attribution anywhere**
    (`CONVENTIONS.md` §2). No `main` branch changes.

Slice 0 is **independent of open questions Q12–Q15** — do not let any of them influence this slice.

---

## 1. Module & toolchain

- **Module path:** `github.com/ArdurAI/sith`
- **Go version:** `go 1.24` (the `go` directive in `go.mod`). CI pins `1.24.x`.
- **Init:**
  ```bash
  cd /Volumes/EXTENDED/repos/sith
  go mod init github.com/ArdurAI/sith      # if go.mod absent
  ```
- **Dependencies (resolve exact versions with the toolchain, do not hand-pin):**
  ```bash
  go get github.com/spf13/cobra@latest     # CLI framework
  go get gopkg.in/yaml.v3@latest           # config file parsing
  go mod tidy
  go mod verify
  ```
  Everything else is the standard library (`log/slog`, `context`, `encoding/json`, `runtime`,
  `os`, `flag`-free — cobra owns flags).

### CLI framework: **cobra** (decision, justified)

Use **`github.com/spf13/cobra`**. Rationale, briefly: it is the de-facto standard for Go
operational CLIs — `kubectl`, `helm`, `k9s`, `argo`, and `gh` all use it — so it matches the
mental model of Sith's exact audience; it gives a clean subcommand tree, POSIX flags (via `pflag`),
built-in help, and shell completion for free; and it scales to the `sith`/`sith ui`/`sith hub`/
`sith serve --mcp` tree without rework. **Not viper:** Slice 0's config needs are tiny, and viper
pulls a large transitive tree that inflates the SBOM and supply-chain surface (a first-order
concern for a cosign/SLSA/SBOM project, E9). We hand-roll a ~60-line config loader over `yaml.v3`
instead (§ `internal/config`); revisit viper only if config genuinely grows. **Not urfave/cli or
stdlib `flag`:** less ecosystem alignment and a thinner subcommand/ completion story for the tree we
need.

---

## 2. Directory & file layout (create exactly this)

```
sith/
├── go.mod                              # module + go 1.24 + require cobra, yaml.v3
├── go.sum
├── Makefile                            # §5
├── .golangci.yml                       # CONVENTIONS.md §4.2 (v2 schema) — copy verbatim
├── .gitignore                          # extend existing: add /bin/, *.out, coverage.*
├── .github/
│   └── workflows/
│       └── ci.yml                      # §6
├── cmd/
│   └── sith/
│       └── main.go                     # tiny entrypoint → internal/cli.Execute()
├── internal/
│   ├── buildinfo/
│   │   ├── buildinfo.go                # Version/Commit/Date vars (ldflags) + String()/JSON()
│   │   └── buildinfo_test.go
│   ├── config/
│   │   ├── config.go                   # Config struct + Load() precedence loader
│   │   └── config_test.go
│   ├── logging/
│   │   ├── logging.go                  # slog logger builder
│   │   └── logging_test.go
│   ├── fleet/
│   │   ├── model.go                    # typed fleet model: Cluster, FleetResult, Coverage
│   │   ├── source.go                   # the Source interface — the F2.1 seam (#38)
│   │   ├── stub.go                     # StubSource: returns an empty FleetResult
│   │   └── fleet_test.go
│   └── cli/
│       ├── root.go                     # root cmd, persistent flags, wiring, Execute()
│       ├── version.go                  # `sith version`
│       ├── clusters.go                 # `sith clusters` (uses fleet.Source)
│       ├── ui.go                       # `sith ui` stub
│       ├── hub.go                      # `sith hub` stub
│       └── cli_test.go
├── sessions/                           # scaffold ALREADY EXISTS (from the docs/build-plan branch)
│   ├── README.md                       # present — do not recreate
│   ├── JOURNAL-TEMPLATE.md             # present — copy it to start your session
│   └── 2026-07-<dd>-slice-0-foundation.md   # ADD this: your session's journal
└── docs/ …                             # already present; do not modify plan docs
```

Every `.go` file starts with `// SPDX-License-Identifier: Apache-2.0` on line 1, then the package
clause. Package name == directory name.

---

## 3. Package specs (signatures + behavior — the builder implements the bodies)

> These are the **contracts**. Implement idiomatic Go bodies that satisfy them and the tests in §7.
> Snippets are reference signatures, not finished code.

### 3.1 `internal/buildinfo`

Injected via `-ldflags -X` (see Makefile). Defaults must be self-describing when built without
ldflags (e.g. `go run`).

```go
package buildinfo

// These are overwritten at build time via -ldflags -X. Keep the defaults.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info is the resolved build metadata, including runtime-derived fields.
type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Go       string `json:"go"`       // runtime.Version()
	Platform string `json:"platform"` // runtime.GOOS + "/" + runtime.GOARCH
}

func Get() Info          // fills Version/Commit/Date + Go/Platform from runtime
func (i Info) String() string // multi-line human text (see §4 output format)
func (i Info) JSON() (string, error) // compact JSON
```

### 3.2 `internal/config`

Hand-rolled precedence loader. **Precedence (lowest→highest): defaults → config file → env → flags.**

```go
package config

type Config struct {
	LogLevel       string `yaml:"log_level"`       // debug|info|warn|error  (default: info)
	LogFormat      string `yaml:"log_format"`      // text|json              (default: text)
	KubeconfigPath string `yaml:"kubeconfig_path"` // reserved for Slice 1; unused in Slice 0
}

func Defaults() Config

// Load resolves config from (in precedence order) defaults, the YAML file at `path`
// (or the default location if path==""), then env vars, then any non-zero Overrides.
// Env vars: SITH_LOG_LEVEL, SITH_LOG_FORMAT, SITH_KUBECONFIG.
// Default file location: $XDG_CONFIG_HOME/sith/config.yaml, else ~/.config/sith/config.yaml.
// A missing default-location file is NOT an error; a missing explicitly-passed path IS an error.
// Validate() runs at the end: unknown LogLevel/LogFormat -> error (fail-safe, never default-open).
func Load(path string, overrides Overrides) (Config, error)

type Overrides struct { // non-empty fields win (from flags)
	LogLevel  string
	LogFormat string
}

func (c Config) Validate() error
```

Fail-safe (`CONVENTIONS.md` §7.5): an invalid `log_level`/`log_format` returns an error; it is never
silently coerced.

### 3.3 `internal/logging`

```go
package logging

// New builds a *slog.Logger for the given level/format writing to w (usually os.Stderr).
// format: "text" -> slog.NewTextHandler; "json" -> slog.NewJSONHandler.
// level:  debug|info|warn|error -> slog.Level. Unknown level/format -> error.
func New(w io.Writer, level, format string) (*slog.Logger, error)
```

User-facing command output (version text, cluster tables) goes to **stdout via `fmt`**; diagnostics
and structured logs go to the **slog logger (stderr)**. Never mix the two.

### 3.4 `internal/fleet` — the F2.1 seam (the load-bearing part of Slice 0)

This is the interface Slice 1 (F2.1 #38) implements with a real local-kubeconfig adapter and the
hub later implements with an OCM-spoke adapter. Slice 0 ships the **types + interface + a stub**.

```go
package fleet

import (
	"context"
	"time"
)

// Source is the read seam every fleet backend implements. Day-0 local mode provides a
// local-kubeconfig adapter (F2.1/#38); day-N hub provides an OCM-spoke adapter (#9).
// Everything above this interface is shared, one code path over many sources.
type Source interface {
	// Kind identifies the adapter, e.g. "stub", "local-kubeconfig", "ocm-spoke".
	Kind() string
	// Fleet returns the current normalized fleet snapshot for this source.
	Fleet(ctx context.Context) (FleetResult, error)
}

// FleetResult is the normalized snapshot returned by a Source.
type FleetResult struct {
	Clusters []Cluster `json:"clusters"`
	// Coverage makes a partial view impossible to mistake for a complete one
	// (SITH-NOTION.md F2.5): what was asked for, what answered, what did not.
	Coverage Coverage `json:"coverage"`
}

// Cluster is one cluster/context in the fleet, freshness- and source-stamped (F2.2).
type Cluster struct {
	Name       string    `json:"name"`               // display name (context name in local mode)
	Context    string    `json:"context,omitempty"`  // kubeconfig context (local mode)
	SourceKind string    `json:"source_kind"`         // the Source.Kind() that produced it
	Reachable  bool      `json:"reachable"`
	ObservedAt time.Time `json:"observed_at,omitempty"` // zero => never observed
}

// Coverage summarizes reachability across the queried sources/contexts.
type Coverage struct {
	Requested   int      `json:"requested"`
	Reachable   int      `json:"reachable"`
	Unreachable []string `json:"unreachable,omitempty"` // names/contexts that failed
}

// StubSource returns an empty, well-formed FleetResult. It exists so the CLI has a
// complete end-to-end path in Slice 0; Slice 1 replaces it with the local-kubeconfig adapter.
type StubSource struct{}

func (StubSource) Kind() string { return "stub" }
func (StubSource) Fleet(ctx context.Context) (FleetResult, error) {
	return FleetResult{Clusters: []Cluster{}, Coverage: Coverage{}}, nil
}
```

**Do not** add kubeconfig, client-go, or informer logic in Slice 0. The stub is the whole backend.
Keeping the interface minimal-but-correct here is the point: Slice 1 slots in without touching
`cmd/` or `internal/cli/`.

### 3.5 `internal/cli`

Root command + subcommands, using cobra. `Execute()` is the single entrypoint `main` calls.

```go
package cli

// Execute builds the root command and runs it; returns a process exit code.
func Execute() int
```

Root command wiring:
- `Use: "sith"`, short/long descriptions naming ArdurAI and the local fleet client.
- **Persistent flags:** `--log-level` (default from config), `--log-format`, `--config` (path),
  `-o, --output` (`text`|`json`, default `text`).
- `PersistentPreRunE`: load config (`config.Load`) with flag overrides, build the slog logger
  (`logging.New`), stash both in the command context. Any config/logger error aborts with a clear
  message and non-zero exit.
- A single `fleet.Source` is constructed once (Slice 0: `fleet.StubSource{}`) and injected into the
  `clusters` command. Keep this injectable (a package-level var or a field) so Slice 1 swaps the
  stub for the real adapter by changing **one** line.
- `SilenceUsage: true` and `SilenceErrors: true` on the root; handle/print errors in `Execute()` so
  usage noise does not print on runtime errors, and map errors to a non-zero code.

Subcommand behavior (exact, for deterministic tests):

| Command | stdout (text mode) | JSON mode (`-o json`) | Exit |
|---|---|---|---|
| `sith version` | see §4 | `{"version":…,"commit":…,"date":…,"go":…,"platform":…}` | 0 |
| `sith clusters` (empty) | `No clusters found (source: stub — F2.1/#38 not yet implemented).` | `{"clusters":[],"coverage":{"requested":0,"reachable":0}}` | 0 |
| `sith ui` | `sith ui: not yet implemented — see F11.3 (#34).` | same line | 0 |
| `sith hub` | `sith hub: not yet implemented — hub mode is phase-1+ (E1–E10).` | same line | 0 |
| `sith` / `sith --help` | cobra usage | — | 0 |
| unknown subcommand | cobra "unknown command" error to stderr | — | **non-zero** |

`clusters` renders from whatever the injected `Source.Fleet(ctx)` returns; in Slice 0 that is always
empty, but the render code must handle a non-empty result generically (a simple aligned table:
`NAME  CONTEXT  SOURCE  REACHABLE  OBSERVED`) so Slice 1 needs no CLI change.

### 3.6 `cmd/sith/main.go`

```go
package main

import (
	"os"

	"github.com/ArdurAI/sith/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
```

---

## 4. `sith version` output formats (exact)

**Text (default):**
```
sith <version>
  commit:    <commit>
  built:     <date>
  go:        <goversion>
  platform:  <os>/<arch>
```

**JSON (`-o json`):** a single compact line, e.g.
```json
{"version":"dev","commit":"none","date":"unknown","go":"go1.24.4","platform":"darwin/arm64"}
```

Text goes to stdout. JSON goes to stdout. Nothing else on stdout for these commands.

---

## 5. `Makefile` (create verbatim)

```makefile
# Sith — Makefile
SHELL := /usr/bin/env bash

BINARY   := sith
PKG      := github.com/ArdurAI/sith
CMD      := ./cmd/sith
BIN_DIR  := bin
GOLANGCI := golangci-lint

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)

.PHONY: all build test lint fmt fmt-check vet tidy clean run ci help

all: build

build: ## Build the sith binary into bin/
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD)

test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

lint: ## Run golangci-lint (v2)
	$(GOLANGCI) run ./...

fmt: ## Format code (gofmt + goimports via golangci-lint v2 formatters)
	$(GOLANGCI) fmt ./...

fmt-check: ## Fail if formatting/imports would change anything
	gofmt -l . | tee /dev/stderr | (! read)
	$(GOLANGCI) fmt --diff ./...

vet: ## go vet
	go vet ./...

tidy: ## Tidy and verify modules
	go mod tidy
	go mod verify

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

run: build ## Build then run `sith version`
	$(BIN_DIR)/$(BINARY) version

ci: fmt-check vet lint test build ## Run the full CI gate locally

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}'
```

`make fmt`/`fmt-check`/`lint` require golangci-lint v2 on PATH (install:
`https://golangci-lint.run/welcome/install/`, pin the same v2.x as CI). `gofmt` is bundled with Go.

---

## 6. GitHub Actions CI — `.github/workflows/ci.yml` (create verbatim)

Must go green. Runs on PRs into `dev` and pushes to `dev`.

```yaml
name: ci

on:
  push:
    branches: [dev]
  pull_request:
    branches: [dev]

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

env:
  GO_VERSION: "1.24.x"
  GOLANGCI_VERSION: "v2.1.6"   # pin; bump is its own ci: commit

jobs:
  build-test-lint:
    name: build · vet · gofmt · lint · test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          check-latest: true
          cache: true

      - name: Download & verify modules
        run: |
          go mod download
          go mod verify

      - name: gofmt check
        run: |
          diff=$(gofmt -l .)
          if [ -n "$diff" ]; then
            echo "::error::gofmt needed on:"; echo "$diff"; exit 1
          fi

      - name: go vet
        run: go vet ./...

      - name: golangci-lint
        uses: golangci/golangci-lint-action@v7
        with:
          version: ${{ env.GOLANGCI_VERSION }}
          args: run ./...

      - name: format & imports check (golangci-lint fmt)
        run: golangci-lint fmt --diff ./...

      - name: build
        run: go build -trimpath ./...

      - name: test (race)
        run: go test -race -count=1 ./...
```

> The `golangci/golangci-lint-action@v7` step installs golangci-lint on PATH for the subsequent
> `golangci-lint fmt --diff` step. If a builder finds the binary is not on PATH in that step,
> install it explicitly via the official install script pinned to `${GOLANGCI_VERSION}` before the
> fmt step — do not drop the fmt/imports gate.
>
> A **DCO check** and **signed-commit / review** requirements are enforced by branch protection on
> `dev` (`CONVENTIONS.md` §5), configured in the repo settings — not in this workflow file.

Copy `.golangci.yml` verbatim from `CONVENTIONS.md` §4.2 (golangci-lint **v2** schema). Do not
weaken it to pass; write code that satisfies it.

---

## 7. Test scaffolding — the exact test list

All tests are hermetic (no network, no clusters) and pass under `go test -race`. Use table-driven
style where noted. Test cobra commands **in-process** by constructing the root command and setting
its args + capturing its output (do not spawn the binary in unit tests).

### `internal/buildinfo/buildinfo_test.go`
- `TestGetPopulatesRuntimeFields` — `Get().Go` and `.Platform` are non-empty and match
  `runtime.Version()` / `GOOS/GOARCH`.
- `TestStringContainsAllFields` — `Info.String()` contains version, commit, date, go, platform.
- `TestJSONRoundTrips` — `Info.JSON()` unmarshals back into an equal `Info`.

### `internal/config/config_test.go`
- `TestDefaults` — `Defaults()` == `{info, text, ""}`.
- `TestLoadFromFile` — a temp YAML file sets level/format; `Load` reflects it.
- `TestEnvOverridesFile` — `SITH_LOG_LEVEL`/`SITH_LOG_FORMAT` override file values.
- `TestOverridesBeatEnv` — non-empty `Overrides` (flags) beat env.
- `TestMissingDefaultFileIsOK` — no default-location file → no error, defaults returned.
- `TestExplicitMissingPathErrors` — a non-existent explicitly-passed `--config` path → error.
- `TestInvalidLevelRejected` / `TestInvalidFormatRejected` — fail-safe: invalid values → error
  (table-driven).

### `internal/logging/logging_test.go`
- `TestNewTextHandler` — format `text` produces a logger; a logged line is plain text (not JSON).
- `TestNewJSONHandler` — format `json` produces valid JSON lines (parse a captured line).
- `TestLevelFiltering` — at level `warn`, an `Info` line is suppressed and a `Warn` line emitted.
- `TestInvalidLevelOrFormatErrors` — unknown level/format → error (table-driven).

### `internal/fleet/fleet_test.go`
- `TestStubSourceKind` — `StubSource{}.Kind() == "stub"`.
- `TestStubSourceEmpty` — `Fleet(ctx)` returns zero clusters and zero coverage, no error.
- `TestSourceInterfaceSatisfied` — compile-time `var _ Source = StubSource{}` plus a second
  trivial in-memory `Source` (declared in the test) to prove the interface admits multiple
  adapters (the F2.1/OCM-parity guarantee) and flows through the same result type.
- `TestFleetResultJSONShape` — marshaling an empty `FleetResult` yields
  `{"clusters":[],"coverage":{"requested":0,"reachable":0}}` (asserts the CLI JSON contract).

### `internal/cli/cli_test.go`
- `TestVersionText` — `sith version` stdout contains `sith ` and the platform.
- `TestVersionJSON` — `sith version -o json` stdout parses as JSON with all five keys.
- `TestClustersEmptyText` — `sith clusters` stdout is the "No clusters found" line, exit 0.
- `TestClustersEmptyJSON` — `sith clusters -o json` parses to an empty clusters array.
- `TestClustersUsesInjectedSource` — inject a fake `Source` returning 2 clusters; assert the table
  renders both rows (proves Slice 1 needs no CLI change).
- `TestUIStub` / `TestHubStub` — the exact stub lines, exit 0.
- `TestRootHelpExitsZero` — `sith --help` exits 0 and prints usage.
- `TestUnknownCommandNonZero` — `sith bogus` returns a non-zero code.
- `TestInvalidLogLevelFlagFails` — `--log-level nope` aborts non-zero (config fail-safe reaches the
  CLI).

**Optional e2e (behind `//go:build e2e`, not in the default gate):** `TestBinarySmoke` builds and
runs `bin/sith version` + `bin/sith clusters`, asserting output. Add a `make e2e` target if you
include it. Not required for merge.

---

## 8. Build & verify runbook (what the builder runs)

```bash
cd /Volumes/EXTENDED/repos/sith
git checkout -b feat/slice-0-foundation dev

# ... create files per §2–§7 ...

go mod tidy && go mod verify
make fmt          # normalize
make ci           # fmt-check + vet + lint + test -race + build   → must be all green
./bin/sith version
./bin/sith version -o json
./bin/sith clusters
./bin/sith clusters -o json
./bin/sith ui
./bin/sith hub
./bin/sith --help
./bin/sith bogus; echo "exit=$?"   # expect non-zero
```

Commit in small signed increments (suggested checkpoints, each a `[C]` journal entry + matching
`GSTACK-Checkpoint` trailer):
1. `chore(build): go module, gitignore, Makefile, .golangci.yml`
2. `feat(buildinfo): build metadata + version formatting`
3. `feat(config): precedence config loader with fail-safe validation`
4. `feat(logging): slog logger builder`
5. `feat(fleet): source-abstract fleet model + stub source (F2.1 seam, #38)`
6. `feat(cli): root command + version/clusters/ui/hub with cobra`
7. `ci(actions): build/vet/gofmt/lint/test workflow`
8. `docs(sessions): GSTACK journal for slice 0`

Open the PR into `dev`, confirm CI green, and stop (do not merge — the owner reviews).

---

## 9. Acceptance criteria (restated, checkable)

- [ ] `make ci` green locally; GitHub Actions CI green on the PR.
- [ ] `sith version` (text + json) prints injected build metadata.
- [ ] `sith clusters` returns a typed empty `FleetResult` from `fleet.StubSource`; text + json render.
- [ ] `sith ui` / `sith hub` print their stub lines, exit 0; unknown command exits non-zero.
- [ ] `fleet.Source` interface exists exactly as §3.4; a second in-memory adapter satisfies it in
      tests (F2.1 seam proven).
- [ ] All §7 tests present and passing under `-race`.
- [ ] `sessions/` scaffold + this session's journal committed; checkpoints ↔ commit trailers.
- [ ] Commits Conventional + DCO + SSH-signed; **no AI attribution**; `main` untouched.
- [ ] No kubeconfig/client-go/TUI/web/MCP/keychain code — those are Slices 1–6.

---

## 10. Explicit non-goals for Slice 0 (do not build)

Kubeconfig discovery or fan-out (Slice 1) · client-go / informers (Slice 1) · any real cluster read
· the TUI (Slice 2) · cross-cluster search/correlation (Slice 2) · per-pod logs/exec/port-forward/
YAML (Slice 3) · `sith ui` web server or embedded frontend (Slice 4) · keychain / telemetry-egress
test (Slice 5) · MCP server / `sith serve --mcp` (Slice 6) · brew/goreleaser/cosign/SLSA/SBOM
(Slice P) · viper · a database · anything hub/OCM. If you are tempted, it belongs to a later slice —
leave the seam and stop.
