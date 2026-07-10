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
[C] Checkpoint #1: <this commit> — build plan + Slice-0 spec + conventions + sessions scaffold on
`docs/build-plan`; next: open PR into `dev`, hand Slice-0 spec to the Sonnet builder.

---

**Session close:** Phase-L plan locked; Slice-0 spec is self-contained and ready for a fresh builder.
**Open questions touched:** Q12 (hero surface → TUI/CLI first, `sith ui` fast-follow); Q13 (local→hub
upgrade → deferred to phase-1+, seam only); Q14 (local MCP auth → loopback + optional keychain token);
Q15 (telemetry → permanent hard no in Phase L). All defaults recorded in `docs/BUILD-SEQUENCE.md`;
owner may override any without disturbing Slice 0.
