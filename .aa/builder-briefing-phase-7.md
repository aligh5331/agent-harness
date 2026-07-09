# Phase 7 — Spec Audit — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` §14. Before starting, read all six prior ADRs and `phase-N.log` files, and the `forensic-per-phase-audit-template.md` this project has already been using manually (in `.aa/`, if present, or reconstruct its structure from how it's been applied in `phase-N.log` entries — e.g. Phase 6's forensic audit entries are a real worked example of this check's actual output).

## Your task this phase
Implement the two-layer spec audit as real harness capability, not just a prompt template a human runs manually going forward:

1. **Layer 1 — per-phase check.** A mode/invocation that compares a phase's actual output (code + ADR + log) against that phase's own kickoff briefings. This has been done manually via the `forensic-per-phase-audit-template.md` pattern — formalize it as an actual `forensic` agent invocation the harness can run, likely via a new CLI mode (e.g. `--audit-phase N`) that loads the relevant briefings and phase artifacts and runs a forensic-mode session against them.
2. **Layer 2 — full cross-phase audit.** A separate invocation comparing the complete, merged project state against the full original spec — this is the GoBox-style `violations.md` → `fix_plan.md` pattern already proven to work; formalize its invocation the same way (e.g. `--audit-full`).
3. **Output format.** Both audit layers should produce a structured, parseable result (pass/fail per item, not just prose) so a future automation layer (or a human) can act on it without re-reading a wall of text — consider a simple markdown-with-headers convention, consistent with everything else this project already does, rather than inventing new structured output (JSON, etc.) unless there's a clear reason to.

## Constraints
- Reuse the `forensic` agent's existing tool restrictions (read-only: `read_file`, `list_dir`, `write_log`) — don't grant audit invocations broader access than forensic already has everywhere else in this project.
- Don't modify any of the five core internal packages beyond what's needed to add the new CLI mode(s).

## Deliverable
Working code implementing both audit layers as real CLI-invokable harness capabilities, compiling cleanly. Append a `phase-7.log` entry summarizing what was implemented.
