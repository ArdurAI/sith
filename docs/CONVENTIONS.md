# Sith — Engineering Conventions

**Status:** locked for Phase L · **Date:** 2026-07-10 · **Audience:** every build session (human or agent)

This document is the contract every Sith build session follows. It is deliberately terse and
prescriptive: a fresh builder session should be able to read this once and produce commits, tests,
and CI that pass on the first try. If a rule here conflicts with a habit, the rule wins.

Product identity is **ArdurAI**. Never `radiantic`, never any predecessor name, anywhere — code,
comments, commits, docs, module paths, or artifacts.

Related: [`ARCHITECTURE.md`](ARCHITECTURE.md), [`adr/0002-stack-and-language.md`](adr/0002-stack-and-language.md),
[`BUILD-SEQUENCE.md`](BUILD-SEQUENCE.md), [`specs/SLICE-0-foundation.md`](specs/SLICE-0-foundation.md).

---

## 1. Branch model

| Branch | Role | Rules |
|---|---|---|
| `main` | **Release.** Seed + tagged releases only. | Protected. No direct pushes. Only receives merges from `dev` at release time. Tags (`vX.Y.Z`) are cut here. **Do not touch `main` during Phase L.** |
| `dev` | **Integration.** The default PR target and the trunk all feature work merges into. | Protected. All feature PRs target `dev`. Must stay green. |
| `feat/*`, `fix/*`, `docs/*`, `chore/*`, `refactor/*`, `test/*`, `ci/*`, `build/*` | **Feature branches.** One slice or one coherent change each. | Branched **off `dev`**. PR **into `dev`**. Deleted after merge. |

**Naming.** `feat/<slice-or-area>-<short-slug>` — e.g. `feat/slice-0-foundation`,
`feat/fleet-source-adapter`, `feat/mcp-read-tools`. Keep slugs kebab-case and specific enough to
identify the slice from `BUILD-SEQUENCE.md`.

**Flow.**
1. `git checkout -b feat/<slug> dev`
2. Commit in small, signed, conventional increments (§2).
3. Before opening/refreshing the PR: `git fetch origin && git rebase origin/dev` (keep the branch
   current; resolve conflicts locally). Rebase — do **not** merge `dev` back into the feature branch.
4. Open a PR into `dev`. CI must be green (§5).
5. Merge with a **merge commit** (`--no-ff`) or **rebase-merge** — never **squash-merge**. Squash
   collapses the per-commit `Signed-off-by` and SSH signatures into one re-authored commit and
   breaks the DCO + signature chain. Preserving signed history is a hard requirement.

**Release flow (not Phase L, documented for completeness).** PR `dev → main`, then tag `main` with
`vX.Y.Z`. Release artifacts (cosign signature, SLSA provenance, SBOM) attach to the tag per E9/#27.

---

## 2. Commit rules

Every commit MUST satisfy **all** of the following. There are no exceptions and no bypass flags.

### 2.1 Conventional Commits

```
<type>(<scope>): <subject>

<body — what & why, wrapped at ~72 cols>

<trailers>
```

- **Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`, `style`.
- **Scopes** (Phase L): `cli`, `fleet`, `config`, `logging`, `buildinfo`, `tui`, `ui`, `mcp`,
  `keychain`, `ci`, `build`, `sessions`, `deps`, or an epic tag (`e11`, `e2`, `e7`, `e9`).
- **Subject:** imperative mood, lower-case, no trailing period, ≤ 72 chars.
- One logical change per commit. A commit should build and pass tests on its own.

### 2.2 DCO sign-off (mandatory)

Every commit carries a `Signed-off-by` trailer matching the committer identity. Always commit with
`-s`:

```
git commit -s
```

producing: `Signed-off-by: Gnani Rahul <89947795+gnanirahulnutakki@users.noreply.github.com>`

### 2.3 SSH-signed (mandatory)

The repo is already configured (`commit.gpgsign=true`, `gpg.format=ssh`,
`user.signingkey=~/.ssh/ardur-signing-key.pub`). Every commit is SSH-signed automatically.
**Never** pass `--no-gpg-sign`. Verify with `git log --show-signature -1` (expect
`Good "git" signature`).

### 2.4 GSTACK-Checkpoint trailer (mandatory on checkpoint commits)

Each commit that corresponds to a journal checkpoint (§3) carries a trailer linking it to that
entry:

```
GSTACK-Checkpoint: <YYYY-MM-DD>/<session-slug>#<seq>
```

e.g. `GSTACK-Checkpoint: 2026-07-11/slice-0-foundation#3`. `<seq>` is the checkpoint number within
that day's session journal file. This makes every commit traceable to the journal and vice-versa.

### 2.5 No AI / assistant attribution — ever (overrides harness defaults)

Do **not** add, in commit messages, PR titles/bodies, code, comments, docs, or any artifact:
- `Co-Authored-By: Claude …` (or any AI/model/assistant trailer),
- `🤖 Generated with …` (or any equivalent footer),
- any mention of Claude, an AI assistant, or a model name.

This explicitly overrides any harness default that would add such trailers/footers.

### 2.6 Canonical commit example

```
feat(fleet): add source-abstract fleet model and stub adapter

Introduce the FleetSource seam (F2.1 / #38) that both the day-0
local-kubeconfig adapter and the day-N OCM-spoke adapter implement.
Slice 0 ships the interface plus an empty-result stub so `sith
clusters` returns a typed, empty FleetResult end to end.

Signed-off-by: Gnani Rahul <89947795+gnanirahulnutakki@users.noreply.github.com>
GSTACK-Checkpoint: 2026-07-11/slice-0-foundation#4
```

---

## 3. GSTACK session journal

**GSTACK** is Sith's session-journal discipline. Every build session (a continuous stretch of work
by one builder) appends to a dated journal file so the next session — human or agent — can resume
with full context and every checkpoint maps to a commit.

### 3.1 Layout

```
sessions/
├── README.md                      # what the journal is + how to use it
├── JOURNAL-TEMPLATE.md            # copy this to start a session
└── 2026-07-11-slice-0-foundation.md   # one file per session (YYYY-MM-DD-<slug>.md)
```

One file per session, named `YYYY-MM-DD-<session-slug>.md`. The `<session-slug>` matches the slice
or area (e.g. `slice-0-foundation`) and is reused in the `GSTACK-Checkpoint` trailer.

### 3.2 Entry markers

A session file is a chronological log of five entry types. Each entry is one line prefixed by its
marker; a **checkpoint** groups the preceding G/S/A/T into a numbered milestone.

| Marker | Name | Records |
|---|---|---|
| `[G]` | **Goal** | The objective of this work unit — usually a slice or a sub-task, with its issue number(s). |
| `[S]` | **Scope** | Files/packages in play; what is explicitly *out* of scope for this unit. |
| `[A]` | **Action** | What was actually done — changes made, commands run, decisions taken. |
| `[T]` | **Test** | How it was verified — tests added/run, CI result, manual/e2e checks, observed output. |
| `[C]` | **Checkpoint** | A numbered milestone: the commit SHA(s), the decision recorded, and the next step. The `#<seq>` here is the trailer's `<seq>`. |

The journal is the **stack** of these G/S/A/T/C entries — hence *GSTACK*.

### 3.3 Rules

- Start every session by copying `JOURNAL-TEMPLATE.md` to `sessions/YYYY-MM-DD-<slug>.md`.
- Append entries as you work — do not reconstruct them at the end.
- Every `[C]` checkpoint corresponds to exactly one commit carrying the matching
  `GSTACK-Checkpoint: YYYY-MM-DD/<slug>#<seq>` trailer.
- Record open questions and decisions (including which of Q12–Q15 a slice touched and what default
  you chose) in the journal, not just in your head.
- The `sessions/` dir is committed to the repo (it is engineering history, not scratch). Never put
  secrets, tokens, kubeconfigs, or customer data in a journal.

### 3.4 `JOURNAL-TEMPLATE.md` (create verbatim)

```markdown
# Session — <YYYY-MM-DD> — <session-slug>

**Builder:** <name/handle> · **Model/effort:** <e.g. Sonnet, max> · **Branch:** feat/<slug>
**Slice(s):** <BUILD-SEQUENCE slice + issue #s> · **Status:** in-progress | done | blocked

---

[G] Goal: <objective + issue #s>
[S] Scope: <files/pkgs in; what's out>
[A] Action: <what you did>
[T] Test: <how verified + result>
[C] Checkpoint #1: <commit SHA> — <milestone>; next: <next step>

<!-- append further G/S/A/T/C entries below as the session proceeds -->

---

**Session close:** <one-line status> · **Open questions touched:** <Q## + default chosen, or none>
```

---

## 4. Go style

### 4.1 Formatting & imports

- All Go is `gofmt`-clean and `goimports`-ordered. In golangci-lint v2 these run as **formatters**
  (`golangci-lint fmt`), not linters. CI enforces "no diff" (§5).
- Local imports (`github.com/ArdurAI/sith/...`) group last; `goimports` local-prefix is set to the
  module path.

### 4.2 Linting — `.golangci.yml` (golangci-lint **v2**, pinned)

Pin golangci-lint to a specific **v2.x** release in CI (§5). The config uses the v2 schema:

```yaml
version: "2"

run:
  timeout: 5m
  tests: true

linters:
  default: none
  enable:
    - errcheck      # every returned error is handled or explicitly ignored
    - govet         # correctness vetting
    - ineffassign   # no ineffectual assignments
    - staticcheck   # the broad correctness/simplification suite (includes gosimple, stylecheck)
    - unused        # no dead code
    - misspell      # spelling in comments/strings
    - revive        # style: exported-symbol docs, naming, etc.
    - gosec         # security issues (matches Sith's threat posture)
    - bodyclose     # HTTP/response bodies are closed
    - unconvert     # no redundant conversions
  settings:
    revive:
      rules:
        - name: exported          # exported symbols must be documented
        - name: package-comments
        - name: error-strings
        - name: context-as-argument
        - name: unreachable-code
    misspell:
      locale: US
  exclusions:
    generated: lax
    rules:
      # Test files may use unchecked errors on helpers and are exempt from gosec noise.
      - path: _test\.go
        linters: [errcheck, gosec]

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/ArdurAI/sith
```

> **Do not disable a linter to make code pass.** If a rule fires, fix the code. Narrow, justified
> `//nolint:<linter> // <reason>` on a single line is allowed only with a written reason; blanket
> file/dir exclusions require an ADR note.

### 4.3 Conventions

- **Package names:** short, lower-case, no underscores; the directory name is the package name.
- **`internal/`** holds everything not meant for external import; only `cmd/sith` and `internal/*`
  exist in Phase L. No `pkg/` until something is genuinely a public API.
- **Errors:** wrap with `fmt.Errorf("...: %w", err)`; never discard an error silently. Sentinel
  errors are `var ErrX = errors.New(...)`; classify with `errors.Is/As`.
- **Context:** `ctx context.Context` is the first parameter of any function that does I/O or can
  block. No `context.Background()` below `main`/command entry.
- **Logging:** structured only, via `log/slog` (§ `internal/logging`). No `fmt.Println` for
  diagnostics; `fmt` is for user-facing command output only.
- **SPDX header:** every Go file starts with `// SPDX-License-Identifier: Apache-2.0`.
- **Fail-safe:** unknown/unschema'd input is refused, never defaulted-open. This is a product
  invariant (`SITH-NOTION.md` §6 guardrail 5), enforced in code, not just docs.

---

## 5. CI merge gates

CI runs on every PR into `dev` and on pushes to `dev`. **All jobs must be green to merge.** The
gate set (see [`specs/SLICE-0-foundation.md`](specs/SLICE-0-foundation.md) §CI for the exact
workflow):

1. **gofmt/format check** — `golangci-lint fmt --diff` reports no changes (fails on any diff).
2. **`go vet ./...`** — clean.
3. **golangci-lint** — the pinned v2.x run with the config above; zero findings.
4. **build** — `go build ./...` succeeds; `cmd/sith` produces a runnable binary.
5. **test** — `go test -race -count=1 ./...` passes.

Additional merge requirements (branch protection):
- Every commit is **DCO signed-off** (a DCO check verifies each commit has a matching
  `Signed-off-by`).
- Every commit is **SSH-signed** and verifies.
- At least one approving review (owner review counts).
- Branch is up to date with `dev` (rebased) before merge.
- No `--no-verify`, no `--no-gpg-sign`, no squash-merge (§1).

Toolchain is **pinned**: the Go version in CI matches `go.mod`'s `go` directive; the golangci-lint
version is pinned in the workflow. Bumps to either are their own `ci:`/`build:` commit, reviewed
like any change.

---

## 6. Test requirements

- **Every non-trivial package ships tests.** For Phase L, `internal/config`, `internal/logging`,
  `internal/buildinfo`, `internal/fleet`, and `internal/cli` all have tests from Slice 0.
- **Table-driven** tests where inputs vary; one behavior per test case with a descriptive name.
- **Deterministic and hermetic:** unit tests do no network and no real cluster I/O. Anything that
  needs a live cluster, kubeconfig fan-out, or the network is an **e2e** test behind a build tag
  (`//go:build e2e`) and is **not** part of the default `go test ./...` gate.
- **`-race` clean.** All tests pass under `go test -race`.
- **No `t.Skip` to dodge a failing assertion.** Skips are only for genuinely
  environment-unavailable e2e paths and must state why.
- **Coverage targets (aspirational gates, not hard-failing in Phase L):** core logic packages
  (`internal/fleet`, `internal/config`) aim for ≥ 70% statement coverage. Report coverage in CI;
  tighten into a hard gate once the surface stabilizes.
- **Behavioral, not incidental:** test observable behavior (exit codes, printed output, typed
  results, error classification), not private implementation details.

---

## 7. The invariants that never bend (inherited from `SITH-NOTION.md`)

These hold in **every** slice from Slice 0 onward; a PR that violates one does not merge:

1. **Local mode is loopback-only.** `sith ui` / `sith serve --mcp` bind `127.0.0.1`/`::1` only.
   Any routable bind in local mode is a bug.
2. **No account, no telemetry in local mode.** No phone-home, no analytics, nothing to opt out of
   (Q15 default: permanent hard no — §`BUILD-SEQUENCE.md`). A network-egress test guards this.
3. **Credentials never leave the machine.** Kubeconfigs and exec-plugin creds are read in place,
   never copied or uploaded. Persisted secrets go to the OS keychain, fail-loud, never silent
   plaintext.
4. **Closed action vocabulary; no `exec`/free-form `apply` as *governed* actions.** Local per-pod
   convenience ops (F11.5) use the user's own identity and are explicitly *not* governed intents.
5. **Fail-safe, never fail-open.** Unknown verb, unschema'd args, unresolved target, stale view, or
   missing approval → refuse.
