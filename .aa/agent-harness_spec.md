# Go Coding-Agent Harness — Project Spec

## 1. Purpose

A single Go binary that replaces the previous OpenCode-dispatch-via-Hermes setup. Hermes driving OpenCode via CLI proved too brittle to teach reliably. This harness owns the agent loop directly: it calls an OpenAI-compatible model API, executes tool calls itself, and enforces safety/scoping in code rather than trusting model behavior or a wrapping dispatcher.

Primary use case: a five-agent development pipeline (architect, librarian, builder, tester, forensic) working against a real repository, driven by a project spec file (a file of this same kind).

## 2. Non-Goals (explicitly out of scope for v1)

- A2A / cross-language agent networking
- Multi-provider model abstraction beyond OpenAI-compatible (no genai/Anthropic-native translation layer)
- Dev UI / web dashboard — CLI output + SQLite log is the observability surface
- RAG / vector memory / long-term semantic recall across sessions
- Multimodal input (images, audio, PDF) — text and code only
- Custom diff/patch engine — exact-string-match edits only, no line-based patching
- Custom sandboxing runtime — no containerization built into the harness; if isolation beyond directory-scoping is needed later, shell out to Docker/firejail
- Multi-tenant / distributed design — single user, single host
- Web search / external network access beyond the model API call itself (deferred to v2)
- Streaming responses (deferred to v2 — v1 is non-streaming only)
- Parallel tool calls per turn (deferred — v1 is strictly serial)

## 3. Model Layer

- OpenAI-compatible chat completions API only. No other provider format.
- Per-agent configuration (not global): `model_name`, `base_url`, `context_max_tokens`, `temperature`.
- This allows expensive/reasoning-capable models for low-frequency stages (e.g. architect) while cheap models run high-frequency loops (e.g. builder/tester against DeepSeek Flash or a local llama.cpp OpenAI-compatible endpoint).
- Non-streaming for v1: one request, one complete response, parsed in full before proceeding.

### 3.1 Core interface

```go
type LLM interface {
    Call(ctx context.Context, req Request) (Response, error)
}

type Request struct {
    Model     string
    BaseURL   string
    Messages  []Message
    Tools     []ToolDef
    MaxTokens int
}

type Response struct {
    Text      string
    ToolCalls []ToolCall
    Usage     TokenUsage
}
```

## 4. Storage — SQLite (single file, WAL mode)

`PRAGMA journal_mode=WAL;` set on connection open — allows the harness to write events continuously while other processes (delta-detection queries, future eval extraction) read concurrently without lock contention.

This single database serves four purposes at once: session log, audit trail, loop-detection substrate, and future eval-pipeline feed (relational structure chosen specifically so it can migrate into the existing `eval_extractor.py`/`sessions.db` pipeline later).

Design bias: full content over storage efficiency. Rows are allowed to grow large; simplicity of querying and reasoning about state takes priority over storage size, since this is a single-host debug/eval log, not a hot path.

### 4.1 Schema

```sql
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    project TEXT,
    phase INTEGER,
    mode TEXT,                    -- architect/librarian/builder/tester/forensic
    model_name TEXT,
    base_url TEXT,
    context_max_tokens INTEGER,
    resume_count INTEGER DEFAULT 0,
    started_at TEXT,
    ended_at TEXT,
    status TEXT                   -- running/done/halted/error
);

CREATE TABLE files (
    id INTEGER PRIMARY KEY,
    session_id INTEGER REFERENCES sessions(id),
    path TEXT NOT NULL,
    content_hash TEXT,            -- hash of content after most recent write
    last_event_id INTEGER,
    write_count INTEGER DEFAULT 0,
    UNIQUE(session_id, path)
);

CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    session_id INTEGER REFERENCES sessions(id),
    turn_index INTEGER,
    event_type TEXT,              -- model_call/tool_call/tool_result/text/halt
    tool_name TEXT,                -- nullable: edit_file/bash_exec/read_file/create_file/list_dir
    file_id INTEGER REFERENCES files(id),  -- nullable
    args_json TEXT,
    result_json TEXT,
    tokens_used INTEGER,
    created_at TEXT
);

CREATE INDEX idx_events_session ON events(session_id, turn_index);
CREATE INDEX idx_files_session_path ON files(session_id, path);
```

## 5. Tools (v1 fixed set)

| Tool | Behavior |
|---|---|
| `read_file(path)` | Full content + line numbers |
| `edit_file(path, old_str, new_str)` | Exact string match replace. Fails loud if `old_str` not found. Fails loud and asks for more surrounding context if `old_str` matches more than once — never silently replaces multiple occurrences |
| `create_file(path, content)` | Fails if path already exists — forces explicit intent, no silent overwrite |
| `bash_exec(command, timeout_seconds)` | Runs with `cmd.Dir` set to project root, hard timeout via context, captures stdout+stderr+exit code |
| `list_dir(path)` | Shallow listing |
| `write_log(content)` | Writes only to the fixed `phase-N.log` path for the current phase step. Not a general file-write tool — cannot target arbitrary paths. Granted unconditionally to every agent regardless of other tool restrictions (see §6.2, §15) |

No semantic/AST-aware editing, no multi-file atomic transactions, no dedicated search tool (covered by `bash_exec` + `rg`/`grep`) in v1.

### 5.1 bash_exec output cap

No cap in v1 — log full stdout/stderr. Determine a real cap later from observed p95 output size across real sessions (rough starting guess if needed before data: ~50KB truncation threshold, with truncation explicitly marked in the output rather than silently cut).

## 6. Safety — Path Scoping and Per-Agent Tool Access

### 6.1 Project-root enforcement (hard harness, all agents)

Every file-touching tool call resolves through a root-scoping check before execution:

```go
func resolveScoped(root, path string) (string, error) {
    abs, err := filepath.Abs(filepath.Join(root, path))
    if err != nil {
        return "", err
    }
    absRoot, _ := filepath.Abs(root)
    if !strings.HasPrefix(abs, absRoot+string(os.PathSeparator)) && abs != absRoot {
        return "", fmt.Errorf("path %q escapes project root", path)
    }
    return abs, nil
}
```

This is enforced at the harness level regardless of what the model is told — never trusted to prompt instructions alone.

**Known gap:** `bash_exec` cannot be fully path-scoped this way, since a shell command can `cd`/redirect outside the intended directory on its own. This is an accepted, documented limitation for v1, not a solved problem. If airtight isolation is needed later, the answer is shelling out to Docker/firejail — not building a custom sandbox.

### 6.2 Per-agent, per-tool glob scoping

Beyond project-root enforcement, each agent's config can further restrict *which paths* it may touch per tool:

```yaml
tools:
  read_file: {}
  list_dir: {}
  edit_file:
    paths: ["docs/adr-*.md"]
  create_file:
    paths: ["docs/adr-*.md"]
  bash_exec: null          # not granted at all
```

A tool entirely omitted or set to `null` means the agent does not have that capability — not restricted by prompt instruction, but absent from the tool list presented to the model at all.

**Forensic agent specifically**: denied `bash_exec`, `edit_file`, and `create_file` entirely. Forensic's role is investigation/reporting; it gets `read_file` and `list_dir` only. This closes the class of problem where an investigation pass wrote to unintended locations (e.g. via `bash_exec` redirection to a temp path) — the fix is removing the capability, not tightening prompt wording.

## 7. Loop / Halt Detection

Two modes, both reading the same `files`/`events` tables — no separate bookkeeping structures.

1. **Hardcoded**: `files.write_count` against a threshold (e.g. same file edited N+ times) triggers an immediate halt with no model call needed. `content_hash` comparison before/after a write additionally catches "rewrote identical content" as a stronger loop signal than raw count alone.
2. **Delta/semantic**: recent rows from `events` for the session are serialized into a compact form and given to the model with a narrow, structured question — "does this look like a loop, yes/no + why" — cheap because it's a small targeted prompt, not a full transcript replay.

Checks run after every tool result, before the next model call — a halt costs zero extra model calls.

### 7.1 Max turns

Not a fixed guess for v1. Real value to be derived from actual session data once available:
- Track cumulative `tokens_used` per session (already logged in `events`)
- Real stop condition: `cumulative_tokens > context_max_tokens * 0.85` triggers compaction/halt — this will bind before turn count does in most cases
- `max_turns` exists only as a generous safety backstop (e.g. 40–50), not the primary control
- Set the real constant after 10–20 logged sessions show the actual turns-per-task distribution

## 8. Retry / Backoff Behavior

| Error class | Behavior |
|---|---|
| Timeout | Exponential backoff, capped at 5 minutes total, then log + trigger session-reuse sequence (§9) + exit |
| Rate limit (429) with `retry-after` header | Honor the header value directly instead of guessing via backoff |
| Quota/credit exhausted (e.g. `insufficient_quota`) | Not auto-retried — halt, log, and require user input (top-up / switch provider) before resuming |
| Malformed or empty response (200 status, garbage/empty tool_calls JSON) | One immediate retry; halt and log if it repeats. Distinguished from a network error so it isn't silently misread as a file-write loop by the harness's other loop-detection logic |

## 9. Session-Reuse Sequence

On halt (timeout, quota, or repeated malformed response), the same `session_id` row is reused for DB/audit continuity — but the message history sent to the model on resume is **not** a full replay of prior context.

Instead, a fresh initial prompt is built from a templated summary drawn directly from the `events` table via SQL query + `text/template` fill (no LLM call): e.g. "Previous attempt touched files X, Y, Z; last test run failed with N; halted due to timeout at turn 40."

Rationale: if the halt was caused by context pressure approaching the model's ceiling, blindly replaying full history on resume reproduces the same failure. Decoupling "session_id" (an audit/DB concept) from "message history sent to the model" (a context concept) preserves a single continuous audit trail while giving the model a genuinely bounded, fresh context on each resume.

`sessions.resume_count` increments on each reuse, distinguishing attempts within the same logical session for later inspection.

## 10. Config Format and Bootstrap

### 10.1 Format

Markdown + YAML frontmatter, matching the shape of the existing `~/agent-config/agents/*.md` files (minimal migration cost):

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
  edit_file: {}
  create_file: {}
  bash_exec: {}
---
<system prompt body in markdown — fixed, git-tracked, never modified by Phase 0>
```

### 10.2 Bootstrap-then-disk-wins

- Default agent configs and skills are embedded into the binary at build time (`go:embed`), sourced by cloning a dedicated skills/config repo as a build step — no runtime network dependency.
- On first run in a project, if a `.aa/` directory does not exist, the embedded defaults are extracted to disk.
- Once `.aa/` exists, it is always used as-is — the embedded copy is never consulted again, so hand-edits to system prompts or skills persist across runs.
- Upgrading to newer embedded defaults: delete `.aa/` and re-run to get a fresh extraction. No automatic hash-diffing or partial-merge logic in v1 — deliberately simple, acceptable for single-user personal use.

## 11. Skills

- Convention over new plumbing: a `skills/` directory containing `SKILL.md` files, each with a frontmatter manifest line (name + one-line description) for cheap discovery.
- At session start, the harness does a `list_dir(skills/)` and injects only the manifest lines into context — full skill content is read on demand via `read_file`, not preloaded.
- Sourced the same way as agent configs: cloned at build time from a dedicated repo, embedded via `go:embed`, extracted to `.aa/` on first run, disk-wins thereafter.

## 12. Prompt Structure — System vs. User

Each model call has two distinct prompt sources:

- **System prompt**: the generic, git-committed `agents/*.md` file for that agent type. Fixed. Contains all durable behavioral rules — halt heuristics, known pitfalls, tool-scoping context, safety framing. Never modified by Phase 0 or by any per-project process.
- **User prompt**: entirely produced by the Phase 0 agent (§13), per project, split into two kinds:
  - **Kickoff prompt**: authored once, up front, per phase per agent, during Phase 0 — analogous to how project-specific prompts have historically been hand-written per phase per agent for prior projects (e.g. GoBox). Used verbatim as the user prompt on first entry into that phase/agent.
  - **Loop-iteration template**: for stages that iterate (most commonly builder and tester retry cycles), a template with placeholders filled by `text/template` substitution at runtime from `events`/`files` table data (e.g. last test failure output, prior diff) — no LLM call per iteration. The tester agent in particular is expected to rely on its own kickoff prompt more than a loop-template most of the time, since its task (compare code against tests and spec) does not depend on other agents' output.

Naming convention:
```
.aa/templates/
  architect-briefing-phase-1.md
  librarian-briefing-phase-1.md
  builder-briefing-phase-1.md
  tester-briefing-phase-1.md
  ...
  architect-briefing-phase-2.md
  ...
  builder-template.md      (loop iteration template, generic across phases)
  tester-template.md
  forensic-template.md
```

Not every phase includes every agent — Phase 0 decides, per phase, which subset of the five agents that phase actually needs (e.g. a brownfield bugfix phase may include only builder and tester, with no architect or librarian involvement at all).

## 13. Phase 0 — Prompt Generator Agent

Runs once per project, before any of the five pipeline agents execute. Reads the project's spec file (a document of this kind) and produces three things:

1. **Per-phase stage selection** — for each phase the project will go through, which of the five agents (architect/librarian/builder/tester/forensic) are actually needed. Not every phase needs every agent.
2. **Kickoff prompts** — one user-prompt file per phase per included agent, written up front against the spec (§12).
3. **Loop-iteration templates** — one generic template per agent type that loops, with placeholder slots for runtime-filled data (§12). Tester will often only have kickoff prompts per phase and skip the generic loop template, since its task is spec/test-driven rather than dependent on prior-agent output.

Phase 0 never touches or generates system prompts — those remain fixed, git-tracked files (§12).

## 13.1 Project Phasing (this project)

Since this harness itself has no prior implementation to bootstrap it, the spec is split into phases up front here rather than by a running Phase 0 instance. Sequenced by dependency order:

| Phase | Scope | Depends on | Agents involved |
|---|---|---|---|
| 1 — Foundation | SQLite schema (§4), `LLM` interface + OpenAI-compatible client (§3), basic types. No tools yet — storage and a model-calling stub only. | — | architect, builder, tester |
| 2 — Tool execution & safety | `read_file`/`edit_file`/`create_file`/`bash_exec`/`list_dir`/`write_log` (§5), project-root path scoping, per-agent glob scoping (§6) | 1 | architect, builder, tester |
| 3 — Turn loop & loop detection | Wire tools into the serial turn loop, hardcoded + delta halt checks (§7), retry/backoff (§8), session-reuse sequence (§9) | 1, 2 | architect, librarian, builder, tester |
| 4 — Config & bootstrap | YAML-frontmatter agent config parsing, `go:embed` + `.aa/` extraction (§10), skills convention (§11) | 1 | architect, builder, tester |
| 5 — Git integration | Per-step commit, branch-per-phase, `phase-N.log` generation via `write_log` (§15) | 2, 3 | builder, tester |
| 6 — Phase 0 generator | The meta-agent itself: spec reading, per-phase stage selection, kickoff prompt + loop-template authoring (§12, §13) | 1–5 | architect, builder, tester |
| 7 — Spec audit | Per-phase check + full cross-phase audit implementation (§14) | 6 | builder, tester, forensic |

Each phase runs on its own branch per §15, with the per-phase spec-audit check gating that branch's merge, and the full cross-phase audit running once all phases are merged.

## 14. Spec Audit

Two-layer approach, combining fail-fast local checking with a comprehensive final pass:

1. **Per-phase check**: at the end of each phase's testing step, compare that phase's output against *that phase's own kickoff briefing* (already available, no need to re-read the full spec) — cheap, catches drift close to its source.
2. **Full cross-phase audit**: after all phases complete, run a comprehensive audit of the final state against the complete spec — following the proven pattern already used on prior projects (violations document → fix plan → revised fix plan). Catches inter-component conflicts that a per-phase check cannot see by construction.

No classifier stage attempting to judge "is this failure code or design" — every audit check is a bounded comparison (phase output vs. its own briefing, or final state vs. full spec), consistent with the harness's overall bias toward hard, checkable logic over open-ended model judgment calls at control-flow decision points.

## 15. Git Management

Each step of each phase produces a commit, not just a running SQLite log — the repository's own history becomes a second, human-readable audit trail independent of the database.

- **Per-step commit**: after a phase step completes (a full stage run, not every individual tool call), the harness commits whatever files changed during that step.
- **Per-phase log file**: alongside the commit, `phase-N.log` (e.g. `phase-2.log`) is written — a plain-text summary of what that phase step did, in the acting agent's own words.
- The `phase-N.log` file is committed along with the code changes from that step, so the commit history reads as: code diff + accompanying explanation of what was done and why, per phase.
- **Author and mechanism**: the harness prompts the agent to write this log as a required final step of every phase step — not optional, not left to the model's discretion to remember. Prompted rather than harness-generated so the log carries the agent's own reasoning, not just a mechanical summary of tool calls; required rather than optional so it can't be silently skipped.
- **Access**: writing this log requires its own dedicated tool, `write_log(content)` — not `create_file`/`edit_file`. It writes only to the fixed, predetermined `phase-N.log` path for the current phase, nothing else. This is granted unconditionally to every agent regardless of their other tool restrictions, including forensic — which is otherwise denied all general write access (§6.2). Because `write_log` cannot target arbitrary paths (unlike `create_file`), granting it universally doesn't reopen the hole that forensic's write restriction was closing.
- **SQLite database is never committed.** It is explicitly excluded (`.gitignore`) because it may contain sensitive material surfaced during a run — `.env` contents pulled in via `bash_exec` output, or the full contents of whatever spec file the pipeline was pointed at, both of which can end up in `args_json`/`result_json`. The DB stays local-only; `phase-N.log` is the sanitized, intentionally-written substitute that's safe to publish in git history.

**Commit granularity**: one commit per full phase step — after the acting agent ends its turn, before the next step begins. Not per tool-call-batch. The commit message is authored by the agent that made the changes, even when the only change in that step is the log file itself (no changes to commit is still a commit, with a message explaining why).

**Branching**: each phase runs on its own branch. That phase's per-phase spec-audit check (§14, layer 1 — output compared against its own kickoff briefing) doubles as the PR review for that branch; merging is gated on it passing rather than a separate manual review step. The full cross-phase audit (§14, layer 2) still runs after all phases are merged, as the final comprehensive pass.

## 16. Rollout Plan

- The five pipeline agents (architect/librarian/builder/tester/forensic) plus the Phase 0 prompt generator are built the traditional way first — hand-dispatched, not generated by the new harness itself.
- The new harness is then tested on itself, in a separate branch — i.e., once built, the harness pipeline is pointed at its own repository as a dogfooding exercise, rather than the harness being used to build its own first version.

## 17. Deferred to v2

- Web search / any external network access beyond the model API call
- Streaming model responses
- Parallel tool calls per turn
- Hermes-based invocation contract (v1 invocation is manual CLI only)
- Phase 0 template placeholder syntax specifics and exact manifest format for stage-selection output (to be finalized during implementation)
