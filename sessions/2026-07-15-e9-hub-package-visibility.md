# Session — 2026-07-15 — e9-hub-package-visibility

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/fix/e9-hub-package-visibility`
**Slice(s):** E9 / [#172](https://github.com/ArdurAI/sith/issues/172) · **Status:** in-progress

---

[G] Goal: require organization-admin public package configuration before release, then ensure every
completed release exposes its already verified Hub OCI manifest for normal anonymous, digest-pinned
consumption.

[S] Scope: the one-time organization container-package visibility bootstrap, the release workflow's
anonymous distribution check, static policy tests, and user-facing release documentation. No Hub
runtime behavior, image content, registry credential, Helm values, mutable tag, or new long-lived
secret is introduced.

[A] Action: post-release verification of `v0.3.0-beta.4` proved the signed multi-architecture
manifest, its Cosign bundle, GitHub provenance, SPDX SBOM attestation, and release assets exist,
but an unauthenticated `docker manifest inspect` was denied because the newly created GHCR package
was private. Filed #172 instead of claiming the distribution path was complete.

[A] Action: the strict review correctly identified that GitHub exposes no REST endpoint for package
visibility changes. After keyless image signing and both image attestations succeed, the workflow
now removes its GHCR credentials and requires an anonymous manifest read of the exact digest before
release attachment. The image carries the OCI source-repository label for package linkage. Package
visibility remains the documented one-time organization-admin setting that GitHub requires.

[T] Test: `make ci` passed, including race-enabled unit/integration suites, Go formatting, lint,
vet, vulnerability scan, E2E coverage, the release workflow static policy suite, and the UI latency
gate. `make release-check` passed, including reproducible archives, SBOMs, and a two-platform Hub
OCI layout assembled from the released Linux archives. The targeted shell policy suite and
race-enabled Go release-policy suite also passed. The required real multi-cluster gate
`make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind` passed in 155.576 seconds.

[R] Review: CodeRabbit initially identified the nonexistent package-visibility REST mutation; that
finding was accepted and removed. A later review correctly identified a policy-test gap: the tests
proved the logout and anonymous inspection existed but not that logout preceded inspection. Both
the shell and Go policy suites now enforce that ordering, and the full CI/release validation was
rerun successfully. Two journal-only suggestions were rejected after verification because they
named an unrelated `ardur-proxy` image and incorrectly claimed this not-yet-created PR was merged.
The implementation relies only on the documented package-admin setting plus the executable
anonymous-read gate. The final amended-diff review against `6fe30e0` found zero findings across
the workflow, documentation, policy tests, and this journal.

[S] Security queue: immediately before commit, GitHub reported zero open Dependabot alerts, zero
open code-scanning alerts, and zero open secret-scanning alerts for `ArdurAI/sith`.

[S] Security: GitHub documentation confirms that public Container access is a package-admin setting;
making a public package private again is irreversible. An organization administrator must configure
the package public before a tag is cut. No unsupported REST call or personal token is added. The
local GitHub token lacks optional `read:packages`, and browser session state was not used as an
authentication bypass. The workflow retains only its existing `packages: write` publication
permission and blocks release attachment when its anonymous digest check fails.

[C] Checkpoint #2: local validation and red-team review are complete. Next: record GitHub security
queue evidence, create the signed DCO/GSTACK commit, land the smallest fix PR into `dev`, then
verify exact post-merge CI before requesting the one-time organization-admin visibility bootstrap
and cutting a fresh release tag.
