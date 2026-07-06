# Phase 3 — Turn Loop & Loop Detection — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-1-foundation.md`, `docs/adr-phase-2-tools-safety.md` (including its Fix Cycle 2 section), and both phase logs in full. This phase wires Phase 1's storage/LLM layer and Phase 2's tools together into the actual agent loop — treat both as fixed, merged dependencies.

## Carried-forward item from Phase 2 (do not skip this)
Phase 2's architect identified that `AllowPath` (in `internal/tools/tools.go`) has the same root-resolution-mismatch class of bug that Fix Cycle 2 fixed in `checkPrefix` — but deferred it because `ToolConfig` (which carries `ProjectRoot`) isn't constructed until this phase's turn loop exists. **You are now the one constructing `ToolConfig`.** Ensure `ToolConfig.ProjectRoot` is always the `EvalSymlinks`-resolved root, not a raw path, so `AllowPath`'s glob matching doesn't reintroduce the exact bug Phase 2 just fixed elsewhere. This isn't optional — it's a known gap with a known fix, just waiting on this phase's code to exist.

## Your task this phase
Design the turn loop, halt detection, retry/backoff, and session-reuse mechanics (spec §7, §8, §9).

## What to design

1. **The turn loop itself.** Serial tool calls, non-streaming (per spec — both already decided, don't revisit). Rough shape from spec §9-adjacent discussion:
   ```go
   for turn := 0; turn < maxTurns; turn++ {
       resp, err := llm.Call(ctx, req)
       // log model_call event
       if len(resp.ToolCalls) == 0 { break }
       for _, tc := range resp.ToolCalls {
           result := dispatch(tc, registry, toolConfig)
           // log tool_call + tool_result events
           if halt, reason := checkHaltConditions(db, sessionID, tc); halt {
               // log halt event, return
           }
           req.Messages = append(req.Messages, toolResultMessage(tc, result))
       }
   }
   ```
   Finalize this: exact function boundaries, how `ToolConfig`/registry get constructed per-agent at loop start (this is where the carried-forward `ProjectRoot` resolution matters), how tool results get formatted back into `Message` content the model can use (especially `edit_file`'s `ErrNoMatch`/`ErrAmbiguousMatch` — Phase 2 made these distinguishable specifically so this phase's loop can surface them clearly to the model).

2. **Halt detection** (§7). Two modes reading `files`/`events`:
   - Hardcoded: `files.write_count` threshold, plus `content_hash` comparison (unchanged-content rewrite as a stronger signal). Phase 1's store package already has the query helpers for this (confirm they're sufficient, or specify what's missing).
   - Delta/semantic: serializing recent `events` rows into a compact prompt, asking the model a narrow yes/no question. Specify the exact query (how many recent rows, what fields), the prompt shape, and how its answer feeds back into the halt decision.
   - Specify the actual threshold values (or make them configurable — if so, where do they live for now, since real config parsing is Phase 4? Hand-constructed Go values/constants are fine for this phase, same pattern as Phase 2's tool restrictions.)

3. **Max turns** (§7.1). Not a fixed guess — implement the cumulative-token-based stop condition (`cumulative_tokens > context_max_tokens * 0.85`) as the primary control, with a generous numeric `max_turns` as a backstop only. Specify where `context_max_tokens` comes from for this phase's tests (hand-constructed per-session config, same pattern as before).

4. **Retry/backoff** (§8). All four categories from Phase 1's `ErrorCategory` (including the Fix Cycle 1 split — `ErrCategoryAuth`, `ErrCategoryUnknown`, plus the original `Timeout`/`RateLimit`/`Quota`/`Malformed`) need defined loop behavior now:
   - Timeout: exponential backoff to 5min cap, then trigger session-reuse (§9) and exit
   - RateLimit: honor `LLMError.RetryAfter` (real now, since Phase 1's Fix Cycle 1 wired up actual header capture) if present, else same backoff pattern
   - Quota: halt immediately, no retry, requires user input
   - Malformed: one immediate retry, then halt like Quota
   - Auth (new category from Fix Cycle 1): specify behavior explicitly — likely same as Quota (halt, needs user to fix config), confirm and document
   - Unknown (new category from Fix Cycle 1): specify — likely bounded retry (e.g. 2-3 attempts) before halting, since these are unclassified but potentially transient

5. **Session-reuse sequence** (§9). `resume_count` column already exists in Phase 1's schema. Design: on halt-with-resume (timeout, malformed-repeated), how the fresh templated-summary context gets built — specify the exact SQL query against `events`/`files` and the `text/template` shape for "previous attempt touched files X, Y; last failure was Z" style summaries. This reuses the same `session_id`, increments `resume_count`, and starts a new message history (not a replay).

## Explicitly out of scope for Phase 3
- No config file parsing — Phase 4. Use hand-constructed per-agent config values.
- No git integration (commits, `phase-N.log` via a real `write_log` call wired into the loop) — Phase 5, though `write_log` the *tool* already exists from Phase 2 and can be called by the model like any other tool in this phase's tests.
- No Phase 0 generator — Phase 6.

## Deliverable
Write your design to `docs/adr-phase-3-turn-loop.md`. Cover all five items above with enough specificity that librarian can write concrete BDD scenarios against it without guessing at behavior, and builder can implement without further design input. Explicitly confirm the `ToolConfig.ProjectRoot` symlink-resolution fix is included. Append a `phase-3.log` entry (via the real `write_log` tool now, since it exists — this is the first phase where the harness can use its own tooling for its own logging, even though the harness itself isn't running this session yet).
