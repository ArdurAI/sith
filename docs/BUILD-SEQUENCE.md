# Sith — Phase-L Build Sequence (the adoption wedge)

**Status:** locked · **Date:** 2026-07-10 · **Scope:** Phase L only (day-0 local fleet client + MCP read)

This is the ordered slice plan for the **Phase-L wedge**: the single `sith` binary a DevOps
engineer runs locally — no account, kubeconfig-based, cache-first fleet view — growing into TUI +
local web UI + per-pod actions + MCP read tools. It is the reshape's centre of gravity
(`SITH-NOTION.md` §3, E11/#29). Federation/hub (E1–E10) is **deferred to phase-1+** and does not
gate any slice here.

Each slice below states: **goal**, **mapped issue(s)**, **dependencies**, **acceptance criteria**,
**how it advances the "runnable + useful today" bar**, and **which open questions (Q12–Q15) it
touches**. Slices are built and merged in order; each ends green on CI and leaves the binary more
useful than the slice before.

> **Ordering validated against the plan.** This sequence matches `SITH-NOTION.md` §3 ("build first"
> wedge list) and the E11 exit criteria (§E11): TUI/CLI cache-first render is the leanest day-0 wow;
> `sith ui` reuses the same fleet model; per-pod table stakes make the tool *complete*; the local
> source feeds the *same* E2 fleet model the hub will use (one code path above the source). **One
> deliberate divergence from the roadmap issue #39:** #39 lists E9 (#27) packaging under Phase L as a
> peer of E11. We fold only the *seed* of E9 (repo scaffold, CI, reproducible `make build`) into
> **Slice 0** and treat full brew/multi-arch/cosign/SLSA/SBOM release engineering (#27) as a
> **parallel track (Slice P)** that can run alongside Slices 1–6 without gating them — because the
> engineer's first `go install`/local build is enough to be "runnable today", and release packaging
> should harden once there is something worth shipping. This is noted for the owner, not assumed
> silently.

---

## Who this is for — the target user's daily surface

Sith is weighted toward what a working DevOps engineer touches **every day**, not toward a feature
matrix. The reference user (GR) works daily in: **Kubernetes**, **Helm**, **ArgoCD**, **Docker**,
**Python/bash**, **Fluentd/Fluent-bit**, **Grafana/Prometheus**, across **multi-cloud AWS/Azure/GCP**,
doing **vulnerability fixes** and **cloud networking**. The sequence below is ordered so the earliest
slices land the highest-frequency tasks: a fast multi-cluster fleet view, pod **logs/exec/describe**,
and cross-cluster search — then the common **observability + GitOps** integrations.

**How the daily stack maps to the plan:**
- **Fleet view + logs/exec/describe/YAML** (the minute-to-minute loop) → Slices 1–3 (this phase).
- **Cross-cluster search + "where is X unhealthy / which clusters run image Y"** → Slice 2 (this phase).
- **Vuln fixes** (fleet-wide "which clusters run image X with CVE Y") → Slice 2's search over the
  F2.4 CVE facts; deeper CVE ingestion matures with E2/F2.4.
- **ArgoCD / Grafana / Prometheus / Fluentd read overlays** (see sync state, dashboards, log
  pipelines in-context) → **E12 connector framework (#30)** + **E13 cost overlay (#31)**, fast-follow
  right after the wedge. The wedge deliberately ships the K8s-native loop first; these read
  connectors layer on the same fleet model without disturbing Slices 0–6.
- **Multi-cloud AWS/Azure/GCP** → handled at the source layer: Slice 1's local-kubeconfig adapter
  already honors each cloud's exec-credential plugin (aws/gcloud/az) locally, so a mixed-cloud
  kubeconfig "just works" on day 0.
- **Governed GitOps writes** (ArgoCD sync, PR-open) are **out of Phase L** by design — read before
  write (E4/E5, phase-2+).

> **Hook — fuller GR-workflow profile (incoming).** A richer profile mined from GR's Notion + Claude
> references will be supplied next and folded in here. When it lands, capture it as
> `docs/GR-WORKFLOW-PROFILE.md` and refine (a) the per-slice "User-workflow fit" lines below and
> (b) the E12/E13 connector priority order to match the real frequency data. This section is the
> anchor for that update; do not block the current slices waiting on it.

---

## The locked sequence at a glance

| # | Slice | Mapped issues | Depends on | Hero outcome |
|---|---|---|---|---|
| **0** | Foundation walking-skeleton | E11 #29 (umbrella), E9 #27 (seed) | — | `sith version` / `sith clusters` run; typed empty fleet result through a stubbed source seam; CI green |
| **1** | Source-abstract fleet model + local-kubeconfig adapter + fan-out | **F2.1 #38** + **F11.1 #32** | Slice 0 | `sith clusters` lists **real** kubeconfig contexts; per-context read sessions; unreachable flagged |
| **2** | Cache-first fleet render (CLI + TUI) + cross-cluster search | **F11.2 #33** (+ F11.4 via #10/#38) | Slice 1 | `sith` TUI + `sith get … --all-clusters` render fleet from cache < 100 ms; one cross-cluster query |
| **3** | Per-pod table stakes | **F11.5 #35** | Slice 1 | logs / exec / port-forward / YAML per context with the user's own identity |
| **4** | Local web "fleet IDE" (`sith ui`) | **F11.3 #34** | Slices 1–2 | `sith ui` serves the same fleet model on loopback; embedded frontend |
| **5** | No-account / no-telemetry / keychain custody | **F11.6 #36** | Slice 0 (invariant), Slice 1 | keychain-backed secret custody; network-egress test proves no phone-home |
| **6** | MCP read tools (`sith serve --mcp`) | **F7.1 #37** | Slices 1(-2) | `fleet.inventory/health/correlate/cve-search` read tools over MCP, workspace-scoped, audited |
| **P** | Packaging & supply chain (parallel track) | **E9 #27** | Slice 0 | brew/multi-arch/cosign/SLSA/SBOM — hardens the install funnel; does not gate 1–6 |

**Trust invariants (`CONVENTIONS.md` §7) hold from Slice 0** — loopback-only, no telemetry,
creds-never-leave, fail-safe. They are not a slice; they are enforced continuously and *verified* by
Slice 5.

---

## Slice 0 — Foundation walking-skeleton

**Goal.** Stand up the repo, module, CLI skeleton, config/logging, Makefile, and green CI, and prove
one end-to-end path: `sith version` prints build info, and `sith clusters` returns a **typed, empty**
`FleetResult` from a **stubbed source adapter** — the seam Slice 1 (F2.1) fills. This is the scaffold
everything else attaches to.

**Mapped issues.** No feature issue of its own — it is the substrate under **E11 #29** and seeds
**E9 #27** (repo scaffold + CI + reproducible build). Fully specified in
[`specs/SLICE-0-foundation.md`](specs/SLICE-0-foundation.md).

**Dependencies.** None. Branches off `dev`.

**Acceptance criteria.**
- `go build ./...` succeeds; `make build` produces `bin/sith` with version metadata injected.
- `sith version` prints version / commit / build date / Go version (text and `--output json`).
- `sith clusters` calls the stubbed `fleet.Source`, gets an empty `FleetResult`, and prints a clean
  "no clusters" result with exit 0.
- `sith ui` and `sith hub` are present as stubs that print a clear "not yet implemented — see
  <issue>" notice and exit 0.
- CI is green: gofmt/format check, `go vet`, golangci-lint (v2, pinned), build, `go test -race`.
- `sessions/` exists with `README.md` + `JOURNAL-TEMPLATE.md`; the first session journal is
  committed.

**Advances the bar.** Turns an empty planning repo into a **runnable binary today**. Nothing useful
about the fleet yet, but the skeleton, the source seam, and the CI gate exist — so every later slice
is additive and always-green.

**User-workflow fit.** Workflow-agnostic by design — this is pure scaffold. No GR daily task maps
here; the fit begins at Slice 1.

**Open questions touched.** **None.** Slice 0 is deliberately independent of Q12–Q15.

---

## Slice 1 — Source-abstract fleet model + local-kubeconfig adapter + client-side fan-out

**Goal.** Implement the `fleet.Source` seam for real: a **local-kubeconfig** adapter that
enumerates every kubeconfig context, honors exec-credential plugins locally, opens an independent
non-blocking read session per reachable context, and streams normalized facts into the shared fleet
model (freshness + source stamped). This is simultaneously **F2.1** (the source-abstract model,
#38) and **F11.1** (kubeconfig auto-detect + fan-out, #32) — the local adapter *is* F11.1's fan-out.

**Mapped issues.** **F2.1 #38** (source-abstract fleet model + local-kubeconfig adapter) +
**F11.1 #32** (kubeconfig auto-detect + client-side fan-out). Also lays the `Source` groundwork the
day-N OCM-spoke adapter (#9) reuses unchanged.

**Dependencies.** Slice 0 (the `fleet.Source` interface + `sith clusters` wiring + model types).

**Acceptance criteria** (from #38 / #32 / `SITH-NOTION.md` F2.1, F11.1):
- The fleet model is populated from local kubeconfig contexts with **no hub/OCM**.
- All contexts are detected; each reachable context streams reads; unreachable contexts are
  **surfaced, never fatal** (independent, non-blocking per-context sessions).
- Exec-credential plugins (aws/gcloud/az helpers) run locally; **no credential or kubeconfig is
  copied off the machine** (asserted by the Slice-5 egress test, seeded here).
- Every record is stamped with `observed_at` + `source cluster`; `last_seen` is maintained so a
  cluster that stops reporting is detectable (F2.2/F2.5 shape).
- The same model code serves an OCM-spoke source unchanged (interface parity test — a second
  in-memory adapter satisfies `fleet.Source` and flows through identical model code).
- `sith clusters` now lists real contexts with reachability + freshness.

**Advances the bar.** First moment the tool is **useful**: run `sith clusters` and *see your whole
fleet* — every context, reachable or not — with zero config. This is the wedge's foundation; every
later surface renders this model.

**User-workflow fit.** *"Morning fleet sweep."* GR opens the laptop with AWS EKS, Azure AKS, and
GCP GKE contexts in one kubeconfig; `sith clusters` enumerates all of them at once, running each
cloud's exec-credential plugin locally, and flags any context that's unreachable (expired SSO,
VPN down) — replacing a dozen `kubectl config use-context` + `get nodes` round-trips.

**Open questions touched.** None blocking. (Q13 — local→hub upgrade UX — is the *seam* this model
enables, but it is a phase-1+ concern and does not gate Slice 1.)

---

## Slice 2 — Cache-first fleet render (CLI + TUI) + cross-cluster search

**Goal.** A k9s-style terminal view over the aggregated fleet that renders **from a local cache in
tens of milliseconds** (never spinner-first, never per-keystroke API round-trips), plus scriptable
CLI verbs (`sith get pods -A --all-clusters`) that return the *same* aggregated answers, plus the
wedge's signature cross-cluster query ("every context where `payments` is Degraded", "which contexts
run image X").

**Mapped issues.** **F11.2 #33** (cache-first fleet render, CLI + TUI). Folds in **F11.4** (local
cross-cluster search/correlation), which the roadmap routes through **#10** (correlation query) and
**#38** (source-abstract model) rather than a standalone issue.

**Dependencies.** Slice 1 (the populated, cache-backed fleet model).

**Acceptance criteria** (from #33 / `SITH-NOTION.md` F11.2, F11.4):
- The store is the **single render source**; the API is only a background sync target. Views and the
  command bar (`:`/cmd-K fuzzy nav across all clusters) render **under ~100 ms** from cache; deltas
  reconcile in the background without spinners.
- CLI verbs (`--all-clusters`) return the same aggregated answers as the TUI (parity test).
- One cross-cluster query returns a **correct** answer over **≥ 2 contexts**; any stale/unreachable
  context is **flagged** in the result (coverage never silently dropped — reuses F2.5 staleness).

**Advances the bar.** This is the "**k9s for your whole fleet**" wow. The tool now does the one thing
no OSS tool ships (cross-cluster read + correlation) and does it fast. This is the demo that earns
adoption.

**User-workflow fit.** *"Incident triage across the fleet."* A Prometheus alert fires for
`payments`. Instead of hopping clusters, GR runs `sith` and asks once — "every context where
`payments` is Degraded" — or `sith get pods -A --all-clusters | grep CrashLoopBackOff` from an SSH
box, and gets a fast, cache-first answer with stale clusters flagged. Same query answers the vuln
sweep: "which contexts run image `X`" ahead of a CVE patch (F2.4 facts).

**Open questions touched.** **Q12 (local-mode hero surface).** Default locked here: **TUI/CLI first**
(the leanest day-0 wow), `sith ui` as the fast-follow (Slice 4). Rationale: the TUI and web UI render
the *same* fleet model, so building the cache-first render + query engine first makes the web UI a
thin second surface rather than a parallel effort. If the owner overrides to "web-first" or "both
together", Slices 2 and 4 swap/merge — the model layer (Slice 1) is unchanged either way.

---

## Slice 3 — Per-pod table stakes (logs / exec / port-forward / YAML)

**Goal.** The commodity single-cluster operations whose *absence* drove the Lens exodus — logs,
exec, port-forward, YAML view/edit — run as ordinary K8s API calls against the selected context with
the **user's own kubeconfig identity**. Present so the local tool is *complete*, explicitly **not**
governed typed intents and carrying no fleet-action semantics.

**Mapped issues.** **F11.5 #35** (per-pod table stakes).

**Dependencies.** Slice 1 (per-context client sessions). Independent of Slice 2 (can build in
parallel once Slice 1 lands, but sequenced after it for a clean trunk).

**Acceptance criteria** (from #35 / `SITH-NOTION.md` F11.5):
- Logs (stream + tail), exec (interactive shell into a pod), port-forward, and YAML view/edit work
  per context in local mode, using the user's own identity.
- These paths are **clearly local conveniences**, distinct from the governed action model: they are
  never dispatched as typed intents; the closed vocabulary + no-shell rule still bind every
  *governed* (hub/agent) path (a code-level boundary, asserted by test — local exec must not route
  through any intent/PEP path).

**Advances the bar.** Removes the "but it can't even tail logs" objection. After this slice a Lens/k9s
user can *fully replace* their per-cluster tool with `sith` and additionally get the fleet view.

**User-workflow fit.** *"Debug the failing pod."* Once GR spots the CrashLoop in the fleet view,
the next reflex is `logs -f`, `exec -it` for a quick `curl`/`nslookup` (cloud-networking checks),
`describe`/YAML to see the events and the mounted config, and `port-forward` to hit a Grafana or a
service locally. This slice makes those work per context with GR's own identity — the exact k9s/Lens
loop, now available across the whole fleet without switching tools.

**Open questions touched.** None. (The local-vs-governed distinction is settled by `SITH-NOTION.md`
§6 guardrails, not an open question.)

---

## Slice 4 — Local web "fleet IDE" (`sith ui`)

**Goal.** The same source-abstract fleet model served as a local web UI on **loopback only** — the
visual "Lens-but-better" surface — from the same binary's **embedded** frontend. Single-user,
kubeconfig-direct, no account, no telemetry. Reuses the *same* frontend the hub console (E8) will
serve; here it runs in local mode.

**Mapped issues.** **F11.3 #34** (local web fleet IDE `sith ui`).

**Dependencies.** Slice 1 (model) + Slice 2 (the render/query patterns the web UI mirrors; per-pod
ops from Slice 3 surface here too).

**Acceptance criteria** (from #34 / `SITH-NOTION.md` F11.3):
- `sith ui` serves the aggregated fleet view (multi-cluster views + fleet search/correlation +
  per-pod table stakes) on `localhost`, **binds loopback only** (external bind is refused — invariant
  test), with **no account and no telemetry**.
- It reuses the **same frontend** as the hub console (one codebase, two modes) — the frontend is a
  client of the fleet model with no privileged path (ADR-0002).
- The frontend is embedded in the Go binary (`//go:embed`), so `sith ui` needs no separate asset
  install.

**Advances the bar.** Gives the visual audience (Lens refugees who want a GUI, not a TUI) a reason to
adopt, without a second install or an account wall. Same engine, second face.

**User-workflow fit.** *"Share a view / prefer a GUI."* When GR wants a visual surface — scanning
many namespaces' health at a glance, reading a long YAML, or showing a teammate the fleet during an
incident call — `sith ui` opens the same fleet model in the browser on loopback, no account, no
second install. It's the Lens/Headlamp GUI habit, kept local and multi-cluster.

**Open questions touched.** **Q12** (see Slice 2 — this slice is the "web" arm of the hero decision).
If the owner picks web-first, this slice moves ahead of Slice 2's TUI work; the model layer is shared
regardless.

---

## Slice 5 — No-account / no-telemetry / keychain custody

**Goal.** Make the trust promises **provable**: no login wall, no phone-home, and any local secret
kept in the **OS keychain** (osxkeychain / wincred / secret-service), with a **fail-loud** fallback
(fail or encrypt-at-rest, **never** silent plaintext). The no-account/no-telemetry posture is a
Slice-0 *invariant*; this slice implements the keychain *mechanism* and the *proof* (an egress test).

**Mapped issues.** **F11.6 #36** (no-account / no-telemetry / keychain custody).

**Dependencies.** Slice 0 (invariant enforced from the start) + Slice 1 (there is now a client that
*could* hold a secret). Naturally sequenced just before Slice 6, which is the first feature that
persists a secret (the local MCP token, Q14).

**Acceptance criteria** (from #36 / `SITH-NOTION.md` F11.6):
- **No account** and **no network telemetry** in local mode, **verified by an automated network
  check** (a test that fails if the binary opens any egress connection during a local-mode session
  except to the user's own clusters).
- Any persisted secret goes to the OS keychain; a missing/unavailable keychain **fails loudly or
  encrypts at rest** — never silent plaintext (the gh-CLI mistake). Fallback behavior is asserted by
  test.
- Kubeconfig credentials are read in place, never copied or uploaded (shares the Slice-1 assertion).

**Advances the bar.** Converts "trust us" into "here's the test that proves it". This is the exact
promise that wins the Lens-refugee audience; making it *verifiable* is the differentiator.

**User-workflow fit.** *"Run it on a work laptop without a second thought."* GR points `sith` at
production kubeconfigs holding cloud IAM exec creds; the trust posture — no account, no phone-home
(provable by the egress test), secrets in the OS keychain, creds read in place — is what makes that
safe on a corp machine under security review. It removes the objection before it's raised.

**Open questions touched.** **Q15 (local-mode telemetry stance).** Default locked here: **permanent
hard no** — no telemetry, not even off-by-default opt-in, in Phase L (the Lens backlash argues for
it; `SITH-NOTION.md` Q15). If the owner later wants an explicit, disclosed opt-in, it is a separate
ADR-gated decision, not a silent addition. **Q14** is set up here (keychain is where a local MCP token
would live) and consumed by Slice 6.

---

## Slice 6 — MCP read tools (`sith serve --mcp`)

**Goal.** Expose the fleet model to external agents (Claude Code / Codex / Cursor) as **read** tools
carrying `readOnlyHint: true` — `fleet.inventory`, `fleet.health`, `fleet.correlate`,
`fleet.cve-search` — hitting the same fleet model, scoped to the caller's workspace, audited like any
other read. In local single-user mode there is one workspace (the machine); the tenant-scoping code
path is the *same* one the hub uses (no privileged MCP data path).

**Mapped issues.** **F7.1 #37** (MCP read tools).

**Dependencies.** Slice 1 (model) and ideally Slice 2 (correlation/CVE query engine backs
`fleet.correlate` / `fleet.cve-search`).

**Acceptance criteria** (from #37 / `SITH-NOTION.md` F7.1):
- The four read tools return workspace-scoped fleet answers (including correlation and CVE search),
  carry `readOnlyHint: true`, and are audited.
- MCP reads go through the **same** scope-resolution path as the CLI/UI — the MCP layer has **no
  privileged data path** (a test asserts an MCP read cannot see beyond the caller's scope).
- `sith serve --mcp` binds **loopback only** in local mode (invariant).

**Advances the bar.** Makes Sith useful to the **AI-native engineer** — the fastest-growing adoption
vector. An agent can now answer "which of my clusters run image X with CVE Y?" through governed,
read-only, audited tools, with the exact same enforcement the hub will apply to writes later.

**User-workflow fit.** *"Ask the agent about the fleet."* GR works in Claude Code / Codex daily;
with `sith serve --mcp` the agent can answer "which clusters run image `X` with CVE `Y`?" or "where
is `payments` unhealthy?" through governed, read-only, audited tools — turning the fleet model into
something the AI in GR's editor can query, without ever handing it a cluster credential or a shell.

**Open questions touched.** **Q14 (local MCP auth).** Default locked here: **loopback trust +
optional short-lived local token held in the OS keychain** (Slice 5). Rationale: in single-user local
mode the loopback boundary is the trust boundary; an optional keychain-held token defends against
shadow-MCP local clients without a login wall. The hub-mode signed-token model (E1) is a separate,
later path. If the owner wants per-agent registered identities even locally, that is a superset
built on this seam.

---

## Slice P — Packaging & supply chain (parallel track, E9 #27)

**Goal.** The install funnel: single binary via `brew`/package managers, multi-arch
(`linux/amd64`+`arm64`, `darwin/arm64`), registry-relocatable references, and **cosign-signed
releases + SLSA L2 provenance + SBOM from the first tag**.

**Mapped issues.** **E9 #27** (deployment & packaging — the local-client single-binary funnel).

**Dependencies.** Slice 0 (a buildable binary + CI). **Runs in parallel with Slices 1–6 and gates
none of them** — the divergence from roadmap #39 noted at the top. It becomes *required* before any
public `brew install sith` announcement, not before the binary is useful locally.

**Acceptance criteria** (subset relevant to Phase L, from #27 / `SITH-NOTION.md` E9):
- Reproducible multi-arch release build (goreleaser or equivalent) producing signed binaries.
- cosign signature + SLSA provenance attestation + SBOM attached to each tag.
- A working `brew` formula (tap) installs the binary; `sith version` reports the release metadata.

**Advances the bar.** Turns "runnable today from source" into "`brew install sith && sith` in under a
minute" — the actual adoption on-ramp (`SITH-NOTION.md` §2). Deferred just enough that we harden the
supply chain once there is something worth shipping.

**User-workflow fit.** *"Install it like any other CLI tool."* `brew install sith` (or the distro
package) is the same muscle memory as installing `kubectl`, `helm`, `k9s`, or `argocd` — the
frictionless on-ramp that gets Sith onto GR's machine and, later, teammates'.

**Open questions touched.** None in Phase L (Q3 KMS/HSM reference is a hub/heavy-profile concern,
not the local single-binary funnel).

---

## Open-question dependency summary (Q12–Q15)

| Q | Question (`SITH-NOTION.md` §9) | Slice(s) it touches | Default locked (overridable by owner) |
|---|---|---|---|
| **Q12** | Local-mode hero surface: TUI vs `sith ui` vs both | Slices 2 & 4 | **TUI/CLI first, `sith ui` fast-follow.** Shared model makes either order cheap. |
| **Q13** | Local→hub upgrade UX (graduate a kubeconfig cluster to an OCM minion) | none in Phase L (seam only) | **Deferred to phase-1+.** Slice 1's source-abstract model is the seam; no Phase-L slice blocks on it. |
| **Q14** | Local MCP auth (loopback trust / short-lived token / keychain secret) | Slice 6 (set up by Slice 5) | **Loopback trust + optional short-lived keychain-held token.** |
| **Q15** | Local-mode telemetry stance (permanent no vs later opt-in) | Slice 5 (+ Slice-0 invariant) | **Permanent hard no in Phase L.** Any future opt-in is ADR-gated, disclosed, off-by-default. |

None of Q12–Q15 block **Slice 0**. Q12/Q14/Q15 have safe defaults recorded above and can proceed
without owner input; the owner can override at any point and only the named slices shift.

---

## Sequencing discipline (never violated)

Read before write. The whole Phase-L wedge is **read-only** — there is no governed write path in any
slice here; per-pod ops (Slice 3) are the user's own identity, not Sith-brokered actions. The first
governed write (`gitops.open-pr`, E4/#22) is **P2**, after the hub exists. `exec` as a *governed*
action is never expressible (`SITH-NOTION.md` §6). This ordering keeps the wedge honest: Sith earns
adoption as the tool you use to *see* your fleet, before it ever asks to *act* on it.
