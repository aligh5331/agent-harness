# Full Cross-Phase Spec Audit — Forensic Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` §14, layer 2. This is the comprehensive, once-at-the-end audit — distinct from every per-phase check that's run throughout this project. Run this only once all seven phases are complete and merged.

## Before starting, read
1. `agent-harness_spec.md` in full, start to finish — this is the complete contract the finished harness is being checked against, not any single phase's briefing.
2. Every `docs/adr-phase-*.md`, including all fix-cycle sections.
3. Every `phase-N.log`.
4. The actual codebase — `internal/*`, `cmd/harness/main.go`, `e2e/*` — read it directly.

## Your task
Unlike every per-phase check so far (each scoped narrowly to one phase's own briefings), this audit looks for exactly what those checks structurally cannot see: **inter-component conflicts** — places where two phases individually satisfied their own briefings correctly, but the combination doesn't actually deliver what the full spec requires.

Follow the proven pattern from this project's own history (GoBox's `violations.md` → `fix_plan.md` → `fix_plan_v2.md` cycle): produce a violations document first, not a fix plan — separate the finding from the remediation.

### Specific things to check given this project's actual history
- **Carried-forward items**: this project has a real track record of gaps deferred from one phase and picked up in the next (Phase 2→3's `AllowPath` symlink issue, Phase 3→4's session-ID and delta-call-count decisions). Trace each one from where it was raised to where it was supposedly resolved and confirm the resolution actually holds end-to-end, not just within the phase that claimed to fix it.
- **The audit process itself has a known failure mode**: Phase 6's first forensic audit claimed a scope restriction was correctly enforced when it wasn't — caught only by independent re-verification, not by the audit itself. Don't take any phase's tester/forensic "PASSED" at face value; spot-check the highest-risk claims against the actual code the same way that gap was found.
- **Non-goals boundary**: confirm nothing crept in that the spec explicitly excluded (§2) — A2A, multi-provider abstraction, streaming, parallel tool calls, web search, custom sandboxing.
- **The harness's own dogfooding claim** (§16 rollout plan): this project was bootstrapped by hand-dispatched agents, with the stated intent that the finished harness would then be tested on itself in a separate branch. Confirm whether that dogfooding step has actually happened, or whether it's still outstanding — this is exactly the kind of "spec says X should happen" item a full audit should catch even if no single phase's briefing ever explicitly asked "did dogfooding happen."
- **Git/branching reality vs. spec**: §15 describes branch-per-phase with per-phase-audit-gated merges; this project's actual git history has been a flat commit sequence on `main` (confirmed via `git log` during Phase 5's review, and clarified as intentional — §15 describes what the *finished harness* enforces on projects it manages, not this project's own bootstrap process). Confirm the spec text is unambiguous about this distinction for a future reader who wasn't part of this conversation, since it's a natural point of confusion.

## Deliverable
1. `docs/full-spec-audit-violations.md` — every finding, categorized by severity, each one naming the specific spec section it violates or the specific carried-forward item it traces to.
2. If violations exist: a separate `docs/full-spec-audit-fix-plan.md` proposing remediation, without fixing anything yourself — this audit's job is diagnosis, not repair, matching this project's own established separation between architect (design) and builder (implementation).
3. If no violations exist: state that explicitly and clearly, with the specific list of things you checked and confirmed clean — a full audit that just says "looks good" without showing its work is not more trustworthy than the Phase 6 audit that turned out to be wrong.

Append a `phase-7.log` entry (or a dedicated final-audit log, if that's a cleaner convention by this point) summarizing the outcome.
