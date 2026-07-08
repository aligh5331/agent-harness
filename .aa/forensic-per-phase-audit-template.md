# Per-Phase Spec Audit — Forensic Briefing (Template)

## Project
Go coding-agent harness (or, once Phase 6 exists, whatever project this pipeline is building). Full spec: `agent-harness_spec.md` §14, layer 1. This is the cheap, fail-fast audit that gates a phase branch's merge, distinct from the full cross-phase audit that runs once at the very end (§14, layer 2 — that's a separate, later step, not this one).

**This template is filled in per phase** — replace `{N}` with the phase number being audited and `{scope}` with that phase's short name (e.g. "Foundation," "Tool Execution & Safety") before use.

## Before starting, read (in this order)
1. Every kickoff briefing issued for Phase {N} (architect's, and if applicable librarian's/builder's/tester's) — these are the actual behavioral contract for this phase, not the general spec.
2. `docs/adr-phase-{N}-{scope}.md`, including any Fix Cycle sections.
3. `phase-{N}.log` in full — every agent's entry for this phase, including fix cycles.
4. The actual code changes for this phase (use `list_dir`/`read_file` to inspect the relevant packages directly — don't rely on the log's self-report alone).

## Your task
Compare what was actually delivered against what Phase {N}'s own kickoff briefings asked for — nothing more. This is a bounded, checkable comparison, not an open-ended code review and not a check against the full project spec (that's layer 2's job).

For each item the architect briefing specified as part of this phase's scope:
- Was it actually implemented? (Check the code directly, don't trust the log's claim without spot-checking at least the highest-risk items.)
- Does the implementation match what the ADR specified, or did it silently diverge without being logged as a deviation?
- If builder or tester logged a deviation from the ADR, is it clearly justified and does the justification hold up, or does it look like a shortcut that should have gone back to architect instead?
- Were any items explicitly marked "out of scope for this phase" left alone (not scope creep), and were any "carried-forward" items from a prior phase's audit actually addressed if this phase was supposed to address them?

Do NOT:
- Evaluate this phase's work against later phases' concerns, or against parts of the overall spec this phase's own briefing didn't ask for
- Perform a general code-quality review unrelated to whether the briefing's requirements were met
- Make judgment calls on ambiguous design questions — if something is genuinely ambiguous, name it as a finding for a human decision, don't silently resolve it yourself

## Deliverable
A pass/fail verdict per item in the architect briefing's task list, plus:
- Any concrete gaps (specified but not delivered, or delivered but diverging from spec without justification)
- Any items worth carrying forward to the next phase's audit (mirroring the pattern already used across this project — e.g. Phase 2's `AllowPath` deferral being carried into Phase 3's briefing)
- Explicit confirmation of whether this phase is ready to be considered complete, or what specifically blocks that

Append a `phase-{N}.log` entry documenting the audit findings via `write_log` (or the manual-logging convention if the harness isn't yet self-hosting its own tooling for this session).
