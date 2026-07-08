# Phase 5 — Git Integration — Fix Cycle 1 — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 1" section of `docs/adr-phase-5-git-integration.md` and architect's/builder's latest `phase-5.log` entries.

## Your task this cycle
1. Confirm `tools.test` is no longer tracked (`git ls-files tools.test` returns nothing) and no longer present on disk.
2. Confirm `.gitignore`'s broadened patterns actually prevent recurrence: in a temp test repo, generate a `*.test` binary (`go test -c` in some package) or a stand-in file matching the new pattern, run `gitops.Commit`, and confirm it does NOT get staged/committed — this is the test that actually proves the fix works, not just that the old file is gone.
3. If architect specified a `Commit()`-level safety check, verify it fires correctly; if architect specified the `.gitignore` broadening alone is sufficient, confirm `Commit()` is unchanged from before this fix cycle.

## Regression check
Full existing suite (all packages, ~204+ tests as of Phase 5) still passes.

## Deliverable
Pass/fail report. Append to `phase-5.log`. If clean, Phase 5 is genuinely ready.
