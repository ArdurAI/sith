# Session — 2026-07-10 — build-plan-architecture

**Builder:** GR (architect/lead role) · **Model/effort:** Opus 4.8, max · **Branch:** docs/build-plan
**Slice(s):** Phase-L planning — locks the slice sequence + Slice-0 spec + conventions · **Status:** done

---

[G] Goal: produce the locked Phase-L build plan for the local fleet wedge — `docs/BUILD-SEQUENCE.md`,
`docs/specs/SLICE-0-foundation.md`, `docs/CONVENTIONS.md` — so a Sonnet builder can start Slice 0 with
no extra context. Grounded in `docs/SITH-NOTION.md` and issues #29/#38/#32/#33/#34/#35/#36/#37/#39.
[S] Scope: markdown docs + the `sessions/` GSTACK scaffold only. No product Go code. Off `dev`, PR into
`dev`, do not touch `main`, do not merge.
[A] Action: read SITH-NOTION.md (E2/E7/E9/E11 epics, roadmap map, open questions Q12–Q15), ADR-0002
(Go/single-binary), and all Phase-L issues. Locked the slice order (0→F2.1+F11.1→F11.2→F11.5→F11.3→
F11.6→F7.1, plus parallel packaging track P) and validated it against the doc; recorded the one
divergence from roadmap #39 (E9 packaging folded to a parallel non-gating track). Wrote the three
deliverables + the GSTACK journal scaffold (`sessions/README.md`, `JOURNAL-TEMPLATE.md`).
[T] Test: link-checked relative doc references; confirmed referenced files exist; confirmed signing
config (SSH ED25519, gpgsign on) and branch base (`dev`, tip 6b81428). No code to run this session.
[C] Checkpoint #1: 15def82 — build plan + Slice-0 spec + conventions + sessions scaffold on
`docs/build-plan`; PR #40 into `dev`. next: hand Slice-0 spec to the Sonnet builder.

[G] Goal: weight the build sequence toward GR's real day-to-day — K8s, Helm, ArgoCD, Docker,
Python/bash, Fluentd/Fluent-bit, Grafana/Prometheus, multi-cloud AWS/Azure/GCP, vuln fixes, cloud
networking — and leave a hook for a fuller GR-workflow profile supplied next.
[S] Scope: `docs/BUILD-SEQUENCE.md` only. Slice 0 stays workflow-agnostic. No spec/convention changes.
[A] Action: added a "Who this is for — the target user's daily surface" section (stack → plan mapping)
with a hook to a forthcoming `docs/GR-WORKFLOW-PROFILE.md`; added a concrete "User-workflow fit"
line to every slice (0 agnostic; 1 morning fleet sweep; 2 incident triage + vuln sweep; 3 debug the
failing pod; 4 GUI/share; 5 run safely on a corp laptop; 6 ask the agent; P install like kubectl).
Redone in a dedicated worktree (`/Volumes/EXTENDED/repos/sith-build-plan`) after a concurrent builder
session force-switched the shared main worktree to `docs/f11-local-fleet-ux` and discarded the
uncommitted first pass.
[T] Test: grep confirms 8 per-slice fit lines + the hook; anchors/links intact.
[C] Checkpoint #2: <this commit> — user-workflow weighting folded into BUILD-SEQUENCE; next: fold the
fuller GR-workflow profile into `docs/GR-WORKFLOW-PROFILE.md` when GR supplies it, and re-rank E12/E13
connectors to match.

---

**Session close:** Phase-L plan locked; Slice-0 spec is self-contained and ready for a fresh builder.
**Open questions touched:** Q12 (hero surface → TUI/CLI first, `sith ui` fast-follow); Q13 (local→hub
upgrade → deferred to phase-1+, seam only); Q14 (local MCP auth → loopback + optional keychain token);
Q15 (telemetry → permanent hard no in Phase L). All defaults recorded in `docs/BUILD-SEQUENCE.md`;
owner may override any without disturbing Slice 0.
