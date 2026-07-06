# Phase 2 — Tool Execution & Safety — Fix Cycle 2 — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 2" section of `docs/adr-phase-2-tools-safety.md` and architect's/builder's latest `phase-2.log` entries.

## Your task this cycle

1. **New test case — the one that was missing**: construct a project root where the root path itself sits behind a symlink in its ancestry (e.g. the test's temp directory structure has a symlinked parent component, not just a symlinked child inside the project). Confirm:
   - A legitimate path inside the project (via the symlinked root) resolves correctly and is *not* spuriously rejected as escaping root.
   - An actual escape attempt through that same structure is still correctly rejected.

2. **Regression check**: re-run the full existing Phase 2 suite — plain paths, `..` traversal, absolute-path-outside-root, the original child-symlink-escape test, per-agent glob scoping, `edit_file` failure modes, `bash_exec` timeout — confirm nothing that previously passed now fails as a side effect of touching `checkPrefix`.

3. **Fallback path**: if architect specified a fallback behavior for when `EvalSymlinks(root)` fails, construct a test for that case too (e.g. confirm it doesn't panic and produces a sensible error, or falls back to the prior `Abs`-only behavior as specified).

## Deliverable
Pass/fail report covering the new test, the fallback case, and full regression status. Append to `phase-2.log`. If this passes cleanly, Phase 2 is ready to merge per §15.
