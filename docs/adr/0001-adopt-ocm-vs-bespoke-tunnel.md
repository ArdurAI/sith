# ADR-0001 — Adopt OCM as the substrate (vs. a bespoke tunnel/agent)

**Status:** **Accepted** (Milestone-0 falsification **passed** 2026-07-08) · **Date:** 2026-07-08

## Context

Sith needs a central hub to reach services inside many Kubernetes clusters that live in
isolated networks (different VPCs, behind NAT), **without** the hub holding cluster-admin
credentials and **without** requiring inbound access to each spoke. The naive instinct is
to build a bespoke "outbound-only agent + reverse tunnel + reach cluster-local services".

This mechanism is **not** the differentiator, and it is security-sensitive infrastructure.
Web-verified as of July 2026, it already exists, hardened and maintained:

- **OCM `cluster-proxy`** (**v0.10.0**, 2026-02-02): *"establishes reverse proxy tunnels
  from the managed cluster to the hub cluster … enabling clients from the hub network to
  access services in the managed clusters' network even when all the clusters are isolated
  in different VPCs."* Automates `apiserver-network-proxy` (Konnectivity) on hub + spokes.
- **OCM `managed-serviceaccount`** (**v0.10.0**, 2026-02-02): syncs ServiceAccounts to
  spokes and **projects scoped tokens back to the hub** — i.e., scoped identity without a
  central god-credential.
- The same pattern is proven independently by **Rancher `remotedialer`** (v0.6.1) and the
  **Kubernetes SIG `apiserver-network-proxy`** (Konnectivity).
- **OCM** is a **CNCF Sandbox** framework (accepted 2021-11-09); core `ocm` is **v1.3.1**
  (2026-05-19).

Building this bespoke would re-implement remotedialer/Konnectivity/cluster-proxy worse than
three funded/CNCF efforts.

## Decision

**Adopt OCM as the connectivity + scoped-identity substrate.** Sith depends on
`cluster-proxy` (reach cluster-local services), `managed-serviceaccount` (scoped tokens),
and the `ManagedCluster`/registration APIs. Sith builds **only** the layer above:
read/action/policy federation, governance, and the MCP surface.

**This decision is gated by a falsification test (Milestone-0):**

> Stand up an OCM hub + 2 local spokes (`kind`/`k3d`), enable `cluster-proxy` +
> `managed-serviceaccount`, and reach a spoke's in-cluster Grafana/Argo CD from the hub via
> a scoped MSA token. **If this works in ≤ ~1 day → the "build the transport" scope is
> deleted and this ADR is Accepted. If it does not → the premise is wrong; stop and
> re-evaluate before any product code (ADR moves to Rejected).**

The deciding experiment, its steps, exit criteria, and demo are specified in
[`../ROADMAP.md`](../ROADMAP.md) (Milestone-0). The result is appended here as evidence.

## Consequences

**Positive**
- Deletes ~a year of building/hardening security-sensitive transport.
- Inherits OCM's registration, addon framework, and scoped-token model.
- Keeps the hub free of cluster-admin kubeconfigs (structural blast-radius reduction).
- Aligns with a live CNCF ecosystem (KubeStellar/KubeFlex build on the same substrate).

**Negative / risks**
- **Dependency on OCM's roadmap and addon versions.** Mitigation: pin versions
  (cluster-proxy/managed-serviceaccount v0.10.0), document an update policy (version bumps
  are ADR-gated), track OCM's CNCF maturity progression.
- **Operational learning curve** for OCM (hub/klusterlet/addons). Mitigation: Milestone-0
  is exactly the cheap place to hit this.
- **OCM is Sandbox, not Graduated** — some maturity risk. Mitigation: the addons we depend
  on are precisely the well-exercised ones; abstract the transport behind a thin internal
  interface so an alternative (remotedialer/Konnectivity direct) remains possible.

## Alternatives considered

1. **Bespoke outbound tunnel + agent.** Rejected: commodity, security-sensitive, and we
   would do it worse than OCM/Rancher/Konnectivity. This is the "seductive trap".
2. **Rancher `remotedialer` / Konnectivity directly.** Viable fallback, but OCM bundles
   the tunnel *and* scoped-identity (`managed-serviceaccount`) *and* registration in one
   framework — more of the substrate for free. Kept as an escape hatch behind an interface.
3. **Agentless (upload kubeconfigs to the hub).** Rejected outright: centralizes deep
   credentials — the exact anti-pattern this product exists to avoid.

## Falsification evidence (Milestone-0)

- **Result: ✅ PASS.** A central OCM hub reached an in-cluster service (`nginx.sith-demo`)
  on a managed spoke using a **scoped `managed-serviceaccount` token** — over the
  `cluster-proxy` reverse (konnectivity) tunnel, with the spoke connecting **outbound-only**
  and the hub holding **no admin kubeconfig** for the spoke. The scoping is real, not
  incidental: the same token was **denied** cluster-wide `secrets` and `nodes` (403), while
  the in-scope service/pod reads succeeded — reach and privilege are decoupled. Connection
  tracking on the spoke showed **every** hub-directed flow originating on the spoke to the
  hub kube-apiserver, and **zero** hub→spoke-initiated flows.
- **Setup time: ~15 minutes** of hands-on execution (kind clusters → registration → addons →
  passing reach-test → outbound-only verification), far inside the `≤ ~1 day` exit criterion.
- **Environment:** two single-node `kind` clusters (k8s v1.34.0); `clusteradm` v1.3.1 / OCM
  core v1.3.1; `cluster-proxy` **0.10.0** and `managed-serviceaccount` **0.10.0** (the pinned
  versions above, confirmed latest as of 2026-07-08).
- **Consequence:** the "build the transport/agent" scope is **deleted**. Sith adopts OCM as
  the connectivity + scoped-identity substrate and builds only the federation/governance
  layer above it. **Proceed to Phase 1.**
- **Notes / runbook + verbatim command output:**
  [`../experiments/M0-ocm-falsification.md`](../experiments/M0-ocm-falsification.md).
- One upstream papercut found and worked around (does not affect the verdict): the
  `cluster-proxy` 0.10.0 Helm chart templates a `ManagedProxyConfiguration` field
  (`proxyAgent.additionalValues`) its own bundled CRD does not declare — see the experiment
  doc's "Caveats" for the one-line CRD workaround. Worth filing upstream.
