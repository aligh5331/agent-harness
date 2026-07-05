# Phase 1 — Foundation — Fix Cycle — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-1-foundation.md` (your own prior design) and `phase-1.log` in full — this is a fix cycle on already-tested Phase 1 work, not new design from scratch.

## Context
Tester's E2E pass (42/42) surfaced two real gaps in `internal/llm/openai.go` that weren't caught by the original design, both of which matter because Phase 3's retry/backoff logic (§8 of spec) will be built directly on top of what you decide here:

1. **`parseRetryAfter` is a stub returning 0 unconditionally.** `sashabaranov/go-openai`'s `APIError` type doesn't expose raw HTTP response headers, so the current implementation has no way to read a `Retry-After` header even when the provider sends one. Per spec §8, a 429 with a `Retry-After` header should be honored directly instead of falling back to blind exponential backoff — right now every 429 gets blind backoff regardless.

2. **`ErrCategoryOther` is an undifferentiated catch-all.** It currently receives: genuine unknown/uncategorized errors, network/DNS/TLS failures (`RequestError`), and authentication failures (401 bad API key) that don't match the quota-body heuristic. Spec §8 only defines retry behavior for Timeout/RateLimit/Quota/Malformed — there's no defined behavior for `Other`, and more importantly, a bad API key and a transient network blip are not the same kind of failure. One needs the user to fix their config before any retry will ever succeed; the other might resolve on its own.

## Your task this cycle

**On retry-after capture**: decide how to get access to the real `Retry-After` header. Options to weigh: (a) wrap `go-openai`'s HTTP client with a custom `http.RoundTripper` that captures response headers before `go-openai` parses the body into `APIError`, (b) switch away from `go-openai` for this one concern if it's fundamentally not exposable another way, (c) some other approach you think is cleaner. Pick one and specify exactly how it plugs into the existing `classifyError` function without a wholesale rewrite of `openai.go` — this should be a targeted fix, not a redesign.

**On error category granularity**: decide whether `ErrCategoryOther` should be split (e.g. into `ErrCategoryAuth` for 401/invalid-key cases and a distinct `ErrCategoryUnknown` for everything else), and if so, specify the exact new `ErrorCategory` enum values and which HTTP status codes / go-openai error types map to each. Also specify what §8's retry table should say about the new category — does it halt for user input like Quota does, retry a bounded number of times like a generic transient error, or something else? This directly extends spec §8, so be explicit enough that builder doesn't have to guess and tester has something concrete to verify against.

## Constraints
- This is a fix to existing, tested code — don't restructure `openai.go`'s overall shape (translation functions, compile-time interface check, etc.) unless the fix genuinely requires it.
- Keep the `LLMError` type's existing fields (`Err`, `Category`, `RetryAfter`, `StatusCode`) unless you have a concrete reason to add to them — if you do add a field, justify it.

## Deliverable
Amend `docs/adr-phase-1-foundation.md` with a new "Fix Cycle 1" section covering both decisions above, specific enough for builder to implement without further design input. Append a `phase-1.log` entry via `write_log` summarizing what changed and why.
