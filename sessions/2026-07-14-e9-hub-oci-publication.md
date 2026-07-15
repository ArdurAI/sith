# Session — 2026-07-14 — e9-hub-oci-publication

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e9-hub-oci-publication`
**Slice(s):** E9 / [#169](https://github.com/ArdurAI/sith/issues/169) · **Status:** ready-for-commit

---

[G] Goal: publish the Helm-installable Sith hub only as a release-bound, immutable
multi-architecture OCI image, with verifiable signature, provenance, and SBOM evidence.

[S] Scope: one GHCR repository (`ghcr.io/ardurai/sith-hub`), release-tag-only
`linux/amd64` and `linux/arm64` manifest publication, keyless Cosign signing, GitHub provenance
and SPDX SBOM attestations, release-attached digest evidence, and fail-closed Helm documentation.
No mutable tags, KMS, database, ingress, additional hub capabilities, registry credential in the
repository, or release rewrite path are introduced.

[A] Action: the tag workflow stages only the two GoReleaser Linux archives, builds and pushes the
manifest under the exact release tag, fails closed if that tag exists or cannot be inspected, then
signs and verifies the manifest digest with the exact GitHub Actions workflow identity. It writes
the digest address, its signed blob bundle, SPDX JSON, and provenance/SBOM bundles to the release.

[A] Action: added a non-publishing `hack/verify-release-hub-image.sh` release gate. It creates a
temporary BuildKit builder without changing the developer default, assembles the OCI layout from
the actual release archives, disables Buildx-generated metadata to match production, recursively
walks the OCI index, requires exactly `linux/amd64` and `linux/arm64`, and removes the builder and
temporary files on every exit path.

[A] Action: documented digest-only verification in `docs/RELEASE.md` and `README.md`. Consumers
verify the Cosign identity plus GitHub provenance and the SPDX predicate from the exact immutable
release digest before passing it to the chart; older releases may not contain hub-image assets.

[T] Test: final `make ci` passed formatting, vet, golangci-lint (0 findings), `govulncheck`
(no vulnerabilities), race suites, all safety/policy scripts, binary E2E, and build. Final
`make release-check` passed two reproducible four-platform snapshots, archive verification,
formula rendering, and the archive-derived multi-platform OCI-layout check. The release guard was
also verified against GHCR's actual missing-manifest response.

[T] Test: `make e2e-oci` passed the hardened native and cross-architecture OCI contract.
`make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind` passed the two real-cluster fleet
and hardened image contract in 154.272 seconds. `make e2e-isolation` passed forced-RLS PostgreSQL
coverage and the fixed 50,000-execution cross-workspace fuzz campaign.

[R] Review: the first CodeRabbit uncommitted review reported eight substantive boundary gaps. The
diff now scopes package permission, signing, and each image attestation to their active workflow
blocks; requires the guide's signer workflow; checks the active `release-check` and Buildx command
blocks; rejects mutable hub image references; and guards existing or ambiguous registry tags. A
second review added exact platform parsing, bounded release-job inspection, and guard-before-push
ordering; all three were corrected. Its request to call local `make release-check` from the tag
workflow was rejected: that would run two redundant snapshot releases, not validate the actual
tag build, and materially increase release time. The tag workflow already verifies its own real
GoReleaser distribution before image publication. The final CodeRabbit review reported zero
findings after the metadata-equivalence policy was added.

[S] Security: GitHub queues immediately before the final review were Dependabot 0, code scanning
0, and secret scanning 0. No kubeconfig, credential, token, or local cluster data enters the
workflow, release assets, documentation examples, or session record.

[C] Checkpoint #1: final closeout rechecked Dependabot 0, code scanning 0, and secret scanning 0;
no Kind clusters remained; the temporary release-check builder was removed; and Docker's safe
dangling-image prune reclaimed 1.202 GB. Final `make ci` and `make release-check` remained green.

[C] Checkpoint #2: create the SSH-signed DCO/GSTACK commit, push and land the PR into `dev`, verify
exact post-merge CI, then publish and verify the release tag and GHCR manifest before closing #169
and updating #27/#39.
