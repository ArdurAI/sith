# F11.8 native local desktop shell

Issue: [#166](https://github.com/ArdurAI/sith/issues/166)

Branch: `gnanirahulnutakki/feat/f11-native-desktop-shell`

Base: `origin/dev` at `c6fa47bb63a269025bfcb3ab9f8042bb02d71edb`

## [G] Goal

Add the first native macOS form of Sith's local fleet IDE without creating a
second Kubernetes client, a TCP listener, account state, telemetry, or a path
leak across the UI bridge.

## [D] Decision

- ADR 0010 adopts stable Wails v2 as a thin native shell; Wails v3 remains
  alpha and is not selected.
- The native WebView serves the existing `webui.Application` at the exact
  `wails://wails` origin through Wails' in-process asset-server middleware.
- The only native bridge method opens a directory chooser. It returns success
  or cancellation only; the chosen path and kubeconfig contents never enter
  JavaScript, diagnostics, or persistent state.
- A choice creates a bounded kubeconfig-import source and complete replacement
  in-memory session before atomically replacing the active handler. Failure
  retains the active session.
- `make desktop-build` creates an Apple Silicon development bundle with stable
  `com.ardurai.sith` identity and an ad-hoc signature. Developer ID signing,
  notarization, stapling, and release provenance remain E9 follow-up work.

## [A] Red-team review

- The source passes explicit `wails://wails` only to the existing hardened
  Host/Origin/CSRF/CSP handler; all other non-loopback origins remain rejected.
- `InProcessHandler` holds a read lock for the full request. Replacement waits
  for it, then the prior application closes only after the handler swap, so a
  request cannot observe a closed session.
- `sith desktop --kubeconfig-dir` constructs the bounded directory source
  before the desktop host, avoiding default-kubeconfig hydration before the
  explicit import validates.
- Browser mode has no `window.go` bridge, so the import control remains hidden.
- CodeRabbit's complete staged review found a major close/import race and a
  minor error-wrapping suggestion. The major race is fixed with terminal host
  state and a deterministic regression test. The minor is intentionally not
  applied: native dialog and kubeconfig errors can contain local paths, so the
  bridge and CLI return stable redacted categories. Later CodeRabbit calls
  stopped after remote summarization and retained only the prior findings;
  this is not represented as a fresh clean external-review verdict.

## [T] Tests and evidence

- Focused `go test -race -count=1 ./internal/cli ./internal/webui`: PASS.
- Replacement-preserves-session and in-flight-handler-replacement tests: PASS;
  the pair is stable across 50 local repetitions.
- Final `make ci`: PASS (format, vet, lint, reachable-vulnerability scan with
  no findings, race tests, safety scripts, performance, binary e2e in 18.265s,
  and production build).
- Final `make e2e-isolation`: PASS (forced PostgreSQL RLS tests and 50,000-case
  cross-workspace selector fuzz campaign).
- Final `make release-check`: PASS (two verified Darwin/Linux amd64/arm64
  snapshots, SPDX SBOMs, formula rendering, and deterministic digests).
- Final `make e2e-kind`: PASS in 158.742 seconds for real two-cluster fleet
  fanout and OCI image contracts. `kind get clusters` and the Sith-named Docker
  container check were empty afterward.
- Final `make desktop-build WAILS=/Volumes/EXTENDED/MacData/go/bin/wails`:
  PASS with Wails CLI v2.12.0. The resulting app is ARM64, bundle identifier
  `com.ardurai.sith`, `Signature=adhoc`, and `TeamIdentifier=not set`.
- `go run ./cmd/sith desktop --help`: PASS with the expected desktop and
  `--kubeconfig-dir` contract.
- `git diff --check`: PASS before review/staging.

## [C] Checkpoint

- Implementation, full validation, staged peer/red-team review, PR, merge, and
  exact post-merge CI remain to be recorded as their own signed/DCO checkpoints.
