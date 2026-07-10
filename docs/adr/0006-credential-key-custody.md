# ADR-0006 — Credential & key custody

**Status:** Proposed · **Date:** 2026-07-08

## Context

A cross-fleet control plane is a magnet for secrets. The predecessor's custody model was its
single largest blast radius: **one env-var master key** (`TOKEN_ENCRYPTION_KEY`) decrypted
**every** tenant's kubeconfigs and tokens — one leak (a committed Helm secret, an env dump,
an SSRF reading `/proc/self/environ`, a compromised node) meant unbounded, all-tenant
compromise. It also stored **shared, admin-by-default cluster credentials** centrally
(confused-deputy), and accepted a weak key with no entropy check. The crypto *primitive* was
fine (AES-256-GCM); the **key management** was the catastrophe.

Sith's design already removes most central secrets by construction (ADR-0001, ADR-0005): the
hub holds **no cluster-admin kubeconfigs**; reach uses OCM `managed-serviceaccount` scoped
tokens; execution uses Ardur-brokered short-lived per-action identities re-validated locally.
But some secrets remain — e.g. **Git credentials for `gitops.open-pr`**, the **intent signing
key**, and any integration tokens the hub must hold.

## Decision

1. **No central cluster-admin credentials.** The hub does not store per-cluster admin
   kubeconfigs. Cluster reach = scoped MSA tokens; cluster action = Ardur-brokered
   short-lived identity, verified and executed **locally** by the spoke with its **own**
   identity. (Structurally removes the predecessor's shared-admin blast radius.)
2. **Envelope encryption via a KMS, with per-tenant data keys.** Any secret the hub must
   hold is encrypted with a **per-workspace data key**, itself wrapped by a **KMS/HSM master
   key** (cloud KMS or equivalent). **There is no single process-wide key.** Compromising
   one tenant's data key does **not** expose other tenants; the KMS master key never leaves
   the KMS.
3. **The intent signing key lives in KMS/HSM**, is rotatable, and is treated as the
   highest-value secret (see [`../THREAT-MODEL.md`](../THREAT-MODEL.md) §5). Spoke-side local
   allowlists are the compensating control if it is ever compromised.
4. **Key rotation is first-class:** data keys and the signing key rotate on a schedule and
   on demand; a key-ring supports decrypt-old / encrypt-new during rotation.
5. **Boot-time custody checks:** refuse to start if key material is missing, below an entropy
   floor, or a placeholder; verify KMS reachability. No "changeme" ever accepted.
6. **Secrets never leak to logs/git/errors:** an error sanitizer strips tokens/keys/IPs;
   secrets are never rendered into Helm output committed to git or into log lines; the
   `.gitignore` pre-empts common secret files; this is a **public repo** — nothing sensitive
   is ever committed.
7. **Least standing secret.** Prefer short-lived, per-action, brokered credentials over
   stored long-lived ones wherever possible (`gitops.open-pr` uses the narrowest Git scope
   that can open a PR; no direct-push credential).

## Consequences

**Positive**
- The predecessor's unbounded single-key blast radius is **structurally impossible** —
  per-tenant keys + KMS wrapping bound any leak to one tenant, and the master key never
  leaves the KMS.
- Removing central cluster-admin creds removes the largest standing secret entirely.
- Rotation + boot checks + sanitization close the operational leak paths that actually bit
  the predecessor.

**Negative / cost**
- KMS dependency and envelope logic add operational complexity and a small per-op latency.
  Accepted — this is the difference between bounded and unbounded compromise.
- Per-tenant keys add key-management surface (rotation, lifecycle). Mitigation: a
  well-tested key-ring abstraction; rotation is a primary test target.

## Alternatives considered
- **Single application-managed key (env/secret).** Rejected — the exact predecessor failure;
  unbounded all-tenant blast radius.
- **Per-tenant keys without KMS wrapping** (keys in the DB/app). Better than one key, but the
  key store becomes the single point of compromise. Rejected in favor of KMS-wrapped envelope.
- **Store cluster-admin kubeconfigs centrally, encrypted.** Rejected outright — even
  encrypted, centralizing deep cluster credentials is the anti-pattern the whole product
  avoids (ADR-0001).
- **Full per-tenant DB/schema isolation of secrets.** Possible enterprise-tier hardening;
  orthogonal to envelope encryption and revisited later (see ADR-0003).
