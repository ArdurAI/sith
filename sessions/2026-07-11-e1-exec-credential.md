# E1 ExecCredential passthrough and non-persistence

Issue: [#81](https://github.com/ArdurAI/sith/issues/81)

Branch: `gnanirahulnutakki/test/e1-exec-credential`

## [G] Goal

Close the remaining E1 multi-auth acceptance gap without creating another credential subsystem:
prove that the production local-kubeconfig adapter honors independent client-go ExecCredential
plugins across a mixed AWS/Azure/GCP-style kubeconfig, isolates a broken helper, and never persists
helper output.

## [S] Scope

- Exercise `client.authentication.k8s.io/v1` and `v1beta1` through the production discovery probe.
- Use three independently authenticated HTTPS API endpoints plus one missing helper.
- Return distinct bearer tokens and a valid client certificate/private key from helper stdout.
- Sandbox HOME, temporary, cache, and config roots; scan every resulting file for secret output.
- Inspect fleet discovery output and the adapter's retained `rest.Config` objects for secret output.
- Keep the production adapter unchanged: client-go owns plugin execution, transport injection, and
  in-memory credential rotation.

## [A] Analysis and red-team checks

- Each endpoint rejects every bearer token except its context-specific token; a crossed credential
  therefore makes the healthy-context assertion fail.
- The broken helper shares a healthy API endpoint, proving failure occurs before network access and
  does not block the other contexts.
- The helper receives secret fixtures only from inherited process environment. The kubeconfig
  contains environment-variable names, not returned credential values.
- The helper writes only the ExecCredential document to stdout. It has no marker, cache, or file
  output path.
- The GCP-style fixture returns a parseable private key as well as a token so non-persistence covers
  both sensitive ExecCredential status forms.
- Assertions never print credential values on failure.
- No custom cloud SDK, token cache, kubeconfig copy, plaintext fallback, or new production process
  execution boundary was introduced.

## [T] Tests and evidence

- Targeted exec matrix under the race detector: PASS (`1.09s`).
- Full kubeconfig adapter race suite: PASS; package coverage is `72.3%` in the full gate.
- Privacy boundary race suite: PASS; package coverage is `100.0%`.
- `make ci`: PASS (format, vet, lint, govulncheck, race/coverage, operator-script safety, latency,
  binary e2e, and build).
- `make e2e-isolation`: PASS against digest-pinned PostgreSQL 18.4; hubdb coverage `71.1%`;
  generated-selector campaign completed `82,381` executions.
- `make e2e-kind`: PASS against two real Kubernetes 1.36.1 kind clusters in `89.686s`.
- `make release-check`: PASS with GoReleaser `v2.17.0`, Syft module `v1.46.0`, reproducible
  Darwin/Linux amd64/arm64 archives, SPDX SBOMs, and generated Homebrew formula.
- GitHub security queues before publication: Dependabot `0`, code scanning `0`, secret scanning `0`.
- Cleanup: zero kind clusters; Docker prune reclaimed `1.21 GB`.

## [C] Checkpoint

The acceptance implementation is locally green and ready for signed/DCO publication. PR CI,
merge SHA, post-merge CI, issue closure, and epic/roadmap updates remain pending.
