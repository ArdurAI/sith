# Sith session journals (GSTACK)

This directory is Sith's **session journal**, kept under the GSTACK discipline defined in
[`../docs/CONVENTIONS.md`](../docs/CONVENTIONS.md) §3.

Every build session (a continuous stretch of work by one builder) copies
[`JOURNAL-TEMPLATE.md`](JOURNAL-TEMPLATE.md) to `YYYY-MM-DD-<session-slug>.md` and appends entries as
it works, so the next session — human or agent — resumes with full context and every checkpoint maps
to a commit.

## Entry markers

| Marker | Name | Records |
|---|---|---|
| `[G]` | Goal | The objective of the work unit + issue number(s). |
| `[S]` | Scope | Files/packages in play; what is explicitly out. |
| `[A]` | Action | What was actually done. |
| `[T]` | Test | How it was verified + result. |
| `[C]` | Checkpoint | A numbered milestone: commit SHA(s), decision, next step. `#<seq>` matches the commit's `GSTACK-Checkpoint` trailer. |

The journal is the **stack** of these G/S/A/T/C entries — hence *GSTACK*.

## Rules

- Start a session by copying the template; append as you go (do not reconstruct at the end).
- Each `[C]` checkpoint ⇄ exactly one commit carrying `GSTACK-Checkpoint: YYYY-MM-DD/<slug>#<seq>`.
- Record which open questions (Q12–Q15, `docs/SITH-NOTION.md` §9) a slice touched and the default chosen.
- This directory is committed (engineering history). **Never** put secrets, tokens, kubeconfigs, or
  customer data in a journal.
