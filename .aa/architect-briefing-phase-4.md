# Phase 4 — Config & Bootstrap — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-1-foundation.md`, `docs/adr-phase-2-tools-safety.md`, `docs/adr-phase-3-turn-loop.md` (all sections, including both fix cycles) and all three `phase-N.log` files. This phase replaces Phase 3's hand-constructed `AgentConfig`/`AgentToolConfig`/`ToolRestrictions` structs with real parsing — those Go types are already defined and tested; you are designing what produces them, not redefining them.

## Two carried-forward decisions from Phase 3 (resolve both explicitly in your ADR)

1. **Session ID is not exposed from `loop.Run()`.** Phase 3's tester had to work around this via halt-code parity instead of querying session status directly. Decide: should `HaltReason` gain a `SessionID int64` field, or is there a better place for it? This affects anything doing cost/call accounting or status inspection later — worth deciding now rather than letting Phase 4's config/bootstrap work build around the current gap and making it harder to add later.

2. **Delta check adds an uncounted LLM call every 5th turn.** This isn't a bug, but it's an easy thing to forget when Phase 4 or later work does cost/call accounting. Decide whether this needs a dedicated event type or counter distinct from `model_call`, or whether documenting it clearly (e.g. in the `AgentConfig`/loop package doc comments) is sufficient for now. Your call — this doesn't have to be solved with new code, but it needs a decision, not silence.

## Your task this phase
Design config file parsing (Markdown + YAML frontmatter, §10) and the embedded-defaults bootstrap (`go:embed` + `.aa/` extraction, §10.2), plus the skills convention (§11).

## What to design

1. **Config file format and parser.** Spec §10.1 gives the target shape:
   ```yaml
   ---
   name: builder
   model: deepseek-v4-flash
   base_url: https://api.metisai.ir/v1
   context_max_tokens: 32768
   temperature: 0.2
   tools:
     read_file: {}
     list_dir: {}
     edit_file: {paths: ["docs/adr-*.md"]}
     bash_exec: null
   ---
   <system prompt body in markdown>
   ```
   Design the parser: split on `---` delimiters, YAML-decode the frontmatter into a struct, treat everything after the second `---` as the system prompt string. Specify the Go struct the frontmatter decodes into, and how it maps to Phase 3's existing `loop.AgentConfig` and `tools.AgentToolConfig`/`tools.ToolRestrictions` types — this is a translation layer, not a redefinition. Pay attention to `bash_exec: null` in YAML — confirm how your parser distinguishes "key present with null value" (tool explicitly denied) from "key absent" (also denied, per spec — but confirm the YAML library's behavior for both cases is what you expect, since some YAML decoders treat these differently for maps).

2. **`go:embed` bootstrap** (§10.2). Design:
   - The embed directive location (e.g. a `internal/config/embedded/` package with `//go:embed agents/*.md skills/*`)
   - The `.aa/` directory structure it extracts to on first run
   - The exact "does `.aa/` exist" check and extraction logic — first run only, disk wins forever after, no hash-diffing or partial merge (per spec, deliberately simple)
   - What happens if `.aa/` exists but is missing a specific expected file (e.g. user deleted one agent's config but kept others) — is this an error, or does the harness fall back to embedded defaults for just that missing piece? Decide and document; don't leave it as undefined behavior.

3. **Skills convention** (§11). Design:
   - The `skills/` directory structure and `SKILL.md` manifest-frontmatter format (name + one-line description, matching the pattern of Anthropic's own skills used in this development process, if you want a concrete reference point)
   - How the loop's initial context gets populated with just the manifest lines (via `list_dir` + parsing frontmatter from each `SKILL.md`, not full content) — specify exactly which existing tool calls accomplish this and whether it happens automatically at session start or requires the agent to explicitly look
   - Confirm skills are sourced via the same `go:embed` + `.aa/` bootstrap mechanism as agent configs, not a separate pathway

## Explicitly out of scope for Phase 4
- No git integration (commits, branch-per-phase) — Phase 5
- No Phase 0 generator — Phase 6
- No changes to `internal/loop`'s core turn-loop logic beyond what's needed to accept a parsed `AgentConfig` instead of a hand-constructed one — if the session-ID or delta-call-count decisions above require loop changes, keep them minimal and clearly scoped, don't restructure Phase 3's tested logic

## Deliverable
Write your design to `docs/adr-phase-4-config-bootstrap.md`. Cover: the parser design and struct mappings, the `go:embed`/`.aa/` bootstrap logic including the missing-file edge case, the skills convention, and explicit resolutions for both carried-forward Phase 3 decisions. Append a `phase-4.log` entry via `write_log`.
