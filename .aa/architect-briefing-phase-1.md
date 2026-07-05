# Phase 1 — Foundation — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` in the repo root — read it before starting, it is the source of truth for every decision referenced below.

## Your task this phase
Produce the concrete package layout and interface definitions for the harness's foundation layer: storage and the model-calling client. Nothing in this phase touches tools, the turn loop, or config — those are later phases. Your output is design, not implementation; builder implements against what you produce here.

## What to design

1. **Package layout** for the whole project, since this is the first phase and later phases build on whatever convention you set here. Go-idiomatic, flat where possible — this is a personal single-binary tool, not a library meant for external consumers, so don't over-abstract into more packages than the actual boundaries justify. At minimum you need to account for: storage, model client, (future) tools, (future) config, and the eventual `main`/cmd entrypoint — but only lay out what Phase 1 needs concretely; leave clear seams for the rest rather than pre-building empty packages for later phases.

2. **SQLite schema implementation details.** The spec (§4) gives you the three tables (`sessions`, `files`, `events`) and their columns — your job is the Go side: which SQLite driver (pure-Go, e.g. `modernc.org/sqlite`, avoids cgo entirely — confirm this is what you want given the devbox is a VM with no unusual toolchain constraints), how `PRAGMA journal_mode=WAL` gets set on connection open, migration/schema-creation strategy (embed the `CREATE TABLE` statements and run them idempotently on startup — no separate migration tool needed for a single-file personal-use DB), and the Go struct types that map to each table's rows.

3. **The `LLM` interface and OpenAI-compatible client.** Spec §3 gives you the interface shape (`Call(ctx, Request) (Response, error)`). Decide: which OpenAI-compatible Go client library to build on (`sashabaranov/go-openai` is a reasonable default — confirm or pick an alternative with reasoning), how `Request`/`Response`/`Message`/`ToolDef`/`ToolCall`/`TokenUsage` types are actually shaped in Go, and how errors from the underlying HTTP client get surfaced through `Call` in a way that later phases (§8, retry/backoff) can distinguish timeout vs. rate-limit vs. quota-exhausted vs. malformed-response without the caller needing to inspect provider-specific error internals. You are not implementing retry logic itself in this phase — just make sure the error type you return carries enough information for a later phase to build retry logic on top of it.

## Explicitly out of scope for Phase 1
- No tools (`read_file`/`edit_file`/etc.) — Phase 2
- No turn loop, no halt detection — Phase 3
- No config parsing, no `go:embed` bootstrap — Phase 4
- No git integration — Phase 5
- No actual network calls need to succeed yet against a real provider in this phase's tests — a mock/fake `LLM` implementation satisfying the interface is sufficient for this phase's own testing; real end-to-end calls happen once tools exist in Phase 2+

## Deliverable
Write your design document to `docs/adr-phase-1-foundation.md` (per the spec's Inter-Agent Handoff Convention, §12.1) — this is the actual artifact builder will read before starting, not just a response in this session. It should cover: package layout with one-line justification per package, the finalized Go struct definitions for the three DB tables and the `LLM` interface's supporting types, the chosen SQLite driver and OpenAI client library with brief rationale, and any open decisions you want builder to flag back rather than assume.

Also write a brief entry to `phase-1.log` via `write_log` summarizing what you decided and why, per §15 — this is what tester will read to catch up on this phase's history without rereading the full ADR.
