# ADR 0010: Use a Wails v2 native shell for the local fleet IDE

- Status: Accepted
- Date: 2026-07-14

## Context

Sith already provides a build-free, loopback-only browser IDE through `sith ui`.
Operators also need a macOS application that feels local, including a native folder
chooser for a directory of kubeconfig files. The desktop form must retain the same
source-abstract engine, cache, local-operation boundaries, and privacy posture.

## Decision

Use Wails v2 as a thin macOS shell around the existing Go web UI handler.

- Wails v2 is the upstream stable release line; Wails v3 is alpha and is not used.
- The app serves `webui.Application` through the Wails in-process asset server at
  the exact `wails://wails` origin. It opens no TCP listener.
- The existing API handler, strict Host/Origin checks, per-process CSRF capability,
  CSP, cache, hydrator, and local operation client remain the only implementation.
- The sole native binding opens a directory chooser. It returns only success or
  cancellation to the UI; the selected path and kubeconfig contents never cross
  the UI bridge, persist, or enter diagnostics.
- A successful selection builds a new bounded importer session before atomically
  replacing the current in-memory session. A failing selection leaves the current
  session intact.

## Consequences

The normal CLI remains browser-capable through `sith ui`, while `sith desktop`
opens the same fleet IDE as a local macOS window. `make desktop-build` produces a
development ARM64 `.app` with the stable `com.ardurai.sith` bundle identifier and
an ad-hoc signature. It is deliberately not a public release artifact until E9
supplies Developer ID signing, notarization, stapling, and release provenance.

The first native shell does not add complete Lens parity, telemetry, an updater,
remote control-plane access, Windows/Linux desktop support, or a second Kubernetes
client. Its Wails dependency and macOS runtime therefore become explicit package
review and release-gate responsibilities.

## References

- https://wails.io/docs/introduction/
- https://wails.io/docs/guides/dynamic-assets/
- https://wails.io/docs/guides/signing/
