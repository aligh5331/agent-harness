# Phase 2 — Tool Execution & Safety — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-2-tools-safety.md` and `phase-2.log` (architect's and builder's entries).

## Your task this phase
This phase is almost entirely about safety boundaries holding under adversarial-ish conditions, not just happy-path tool correctness. Test both.

## What to check

1. **Tool correctness (happy path)**
   - `read_file` returns correct content + line numbers
   - `edit_file` correctly replaces on exactly-one-match
   - `create_file` succeeds on a genuinely new path
   - `bash_exec` captures stdout/stderr/exit code correctly for a simple command
   - `list_dir` returns correct shallow listing
   - `write_log` appends correctly to the current phase's log path

2. **`edit_file` failure modes (this is the one the spec is most explicit about)**
   - Zero matches for `old_str` → fails loud, distinguishable error
   - Multiple matches for `old_str` → fails loud, distinguishable error, and — critically — confirm the file is completely unchanged after this failure (no partial/first-match replacement happened before the multi-match check)

3. **Path scoping — try to break it**
   - `../` traversal attempting to escape project root → rejected
   - Absolute path outside root → rejected
   - A symlink inside the project root pointing to a location outside it → test whatever behavior architect specified; confirm it actually matches the ADR, since this is the kind of edge case that's easy to design correctly on paper and get wrong in code
   - A path that resolves to exactly the project root itself → should this succeed or fail? Confirm against the ADR's `resolveScoped` spec, don't assume

4. **Per-agent glob scoping**
   - An agent with `edit_file: {paths: ["docs/adr-*.md"]}` can successfully edit a matching file
   - The same agent is rejected when attempting to edit a file outside that glob (e.g. a `.go` source file)
   - An agent with a tool set to `null`/omitted (e.g. forensic + `bash_exec`) cannot invoke that tool at all — confirm this is enforced at the dispatch layer, not just "the tool exists but always errors for this agent," since the spec's intent is that the model shouldn't even be offered the tool
   - Forensic specifically: confirm it has `read_file`, `list_dir`, `write_log` and nothing else, matching spec §6.2

5. **`bash_exec` timeout behavior**
   - A command that runs longer than the configured timeout is actually killed, not just abandoned while continuing to run in the background — verify the process is actually terminated (e.g. check it's no longer in the process list after timeout), not just that your test code stopped waiting for it

6. **`bash_exec` known gap (don't test as a failure, confirm it's documented)**
   - Confirm the ADR and any user-facing comment explicitly acknowledge that `bash_exec` cannot be fully path-scoped (a `cd ..` inside the command bypasses root-scoping) — this is an accepted v1 gap per spec §6.1, not something you should report as a bug, but you should confirm it's actually documented, not silently absent

## Deliverable
Test suite covering all of the above, pass/fail report, with particular attention to whether any of the "try to break it" tests in sections 3-4 actually succeeded in breaking something — those are the ones that matter most this phase. Append your report to `phase-2.log`. If this passes cleanly, Phase 2 is ready to merge per §15.
