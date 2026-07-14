# Security and OCM upstream monitor — 2026-07-13

[G] Goal: independently verify the ArdurAI/sith security-alert queues and the upstream
ClusterGateway authorization-isolation blocker before allowing any Phase-1 transport work.

[S] Scope: documentation-only security gate on `dev`; no Sith transport code, RBAC policy, or
credential handling changes while the required upstream remediation has not shipped in an official
release.

[A] Action: verified `origin/dev` at `cdb84600e2fd91d54d66368c1ce37b41318d42bd`; Dependabot,
code-scanning, and secret-scanning each report zero open alerts. Upstream
`oam-dev/cluster-gateway` master remains at `2b04dd452b7a9da6e75de105cd00499a1c6369bd` and the
latest official release is `v1.9.1`; neither is a safe consumption point for the authorization
fix. Upstream PR #171 (`5bd9423337eee6bdf10fc29041dcb0e77a7eae21`) removes inbound
`Authorization` before the managed-service-account transport and has green unit, gateway,
OCM-addon, and DCO checks, but remains open.

[T] Test plan: run `make ci`, the real two-cluster kind integration gate, and `make release-check`
from the isolated worktree. The documentation change must leave the working tree clean except for
the intended roadmap and session evidence.

[A] Action: the required `make ci` gate exposed 14 reachable Go standard-library advisories in the
locally installed Go 1.26.0 runtime. `govulncheck` identifies Go 1.26.5 as the minimum fixed patch
level for every finding, so `go.mod` now requires `toolchain go1.26.5`; no vulnerability suppression
was added.

[A] Action: red-team review found that `make release-check` continued after a failed prerequisite
because its shell recipe did not enable fail-fast behavior. Added `set -e` so module verification,
reproducible archive creation, SBOM verification, and digest comparison each gate publication. The
initial local `go mod verify` failure was traced to a host GOPATH containing an unrelated `go.mod`;
the same command passes with an isolated GOPATH and the repository module cache.

[T] Test: `make ci` PASS with Go 1.26.5: formatting, lint, vet, reachable-vulnerability scan,
race suite, M0 harness safety assertions, warm-view performance guard, generic e2e, and build all
passed. `make e2e-kind` PASS with the pinned kind v0.32.0 binary: two real clusters completed in
84.530 seconds and cleaned up. `make release-check` PASS with an isolated GOPATH: module
verification, two reproducible multi-platform archive/SBOM builds, distribution validation, and
digest comparison all completed after fail-fast hardening.

[R] Review: manual red-team diff review found no change to transport implementation, MSA/RBAC
policy, credential logging, or response-body handling. The upstream candidate remains open and no
new official ClusterGateway release exists, so the roadmap preserves the #103/#104 block. The
optional CodeRabbit CLI is not installed locally; no external review payload was sent.

[C] Checkpoint #1: record the release-consumption gate and the minimum secure Go toolchain, then
publish only after the required quality, two-cluster, and release checks pass.
