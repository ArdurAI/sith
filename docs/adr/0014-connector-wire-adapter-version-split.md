# ADR 0014: Split connector wire compatibility from adapter provenance

- **Status:** Accepted
- **Date:** 2026-07-19
- **Issue:** [#288](https://github.com/ArdurAI/sith/issues/288)
- **Parent:** [#30](https://github.com/ArdurAI/sith/issues/30)

## Context

E12 requires an out-of-process, typed, versioned connector framework. The existing internal
connector descriptor has one string field, `ProtocolV`, but landed code uses that field for two
different domains:

- the original source-adapter spec describes one connector-wide semver where minor changes are
  additive and major changes are reviewed; and
- adapters and projectors use opaque evidence or behavior identifiers such as `1.0.0`,
  `alerts/v1`, `search/ecs-v1`, and `gitops-open-pr/2026-03-10`.

Those opaque values are important provenance, but they cannot truthfully drive a future gRPC
compatibility handshake. Repurposing them would require an unrelated migration of existing facts,
fixtures, and persisted evidence before transport exists.

Protocol Buffers allows additive fields as a wire-safe change, but wire safety is narrower than
application-semantic compatibility. It also forbids reusing field numbers and strongly discourages
field-type changes. Sith therefore needs an explicit compatibility policy before it commits a
protobuf service definition.

## Decision

1. Add a structured `WireVersion{Major, Minor}` framework domain. Major zero is invalid.
2. Connector descriptors advertise an explicit set of 1 through 32 supported wire versions. The
   registry rejects malformed, duplicate, or oversized offers and stores a deterministic sorted
   copy.
3. `NegotiateWireVersion` chooses the highest exact version advertised by both endpoints.
4. No common major returns a distinct major-mismatch error. A common major without an explicitly
   shared minor returns a distinct unsupported-minor error. Same major alone never implies support.
5. Rename connector-descriptor `ProtocolV` to opaque `AdapterVersion`. This value continues to
   identify the adapter's evidence and behavior contract and is not parsed as semver.
6. Preserve `fleet.Provenance.ProtocolV` and its serialized `protocol_version` field. Existing
   evidence, persisted facts, and projector protocol identifiers do not migrate in this slice.
7. The initial framework offer is `{major: 1, minor: 0}`.
8. Keep protobuf, generated code, gRPC dependencies, subprocess launch, IPC authentication,
   credentials, networking, persistence, and execution out of this slice.

## Consequences

- The framework can evolve transport independently from every adapter's native/evidence contract.
- Compatibility is deterministic and fail-closed; an opaque provenance value can no longer be
  mistaken for a transport version.
- Version negotiation has a fixed 32-entry-per-endpoint allocation and comparison bound.
- A connector adding minor version 1 must explicitly keep minor version 0 in its offer if it still
  supports it. This is more verbose than range inference but prevents accidental semantic claims.
- Registry descriptor JSON changes before any public out-of-process SDK exists. The package is
  internal and current descriptor consumers are migrated in the same change.
- Existing fleet evidence JSON and database rows remain stable.
- This adds bounded in-memory comparison only. It adds no process, listener, permission, cloud
  resource, telemetry cardinality, or recurring cost.
- Future protobuf work must preserve field numbers, reserve removed fields, avoid required fields,
  and independently define authenticated local IPC, deadlines, health, and bounded restart policy.

## Alternatives considered

- **Repurpose `ProtocolV` as strict semver and migrate all evidence identifiers:** rejected as a
  broad breaking migration before transport exists.
- **Use opaque adapter identifiers directly for the wire handshake:** rejected because major/minor
  compatibility would be undefined and untestable.
- **Infer support for every lower minor in the same major:** rejected because protobuf wire safety
  does not prove application-semantic support.
- **Delay the split until protobuf lands:** rejected because schema work without an accepted
  version-domain boundary would bake the ambiguity into the wire contract.

## Primary sources

- [Protocol Buffers proto3 language and compatibility guide](https://protobuf.dev/programming-guides/proto3/)
- [Protocol Buffers best practices](https://protobuf.dev/best-practices/dos-donts/)
- [gRPC core concepts](https://grpc.io/docs/what-is-grpc/core-concepts/)
