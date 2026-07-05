# ADR-Phase-1 — Foundation Layer

**Status:** Approved (Architect)
**Phase:** 1 of 7
**Date:** 2026-07-04
**Agents:** architect → builder → tester

---

## 1. Package Layout

```
agent-harness/
├── cmd/
│   └── harness/
│       └── main.go          # Phase 1 stub — Open DB, instantiate client, exit.
│                             # Establishes the CLI-entrypoint convention for later phases.
│                             # Minimal content only — sufficient for `go build ./cmd/...` to pass.
├── internal/
│   ├── store/               # SQLite storage: connection, schema, CRUD helpers.
│   │   ├── store.go         #   Store struct, Open/Close, schema migration (idempotent).
│   │   ├── models.go        #   Session, File, Event — Go structs mapping to DB rows.
│   │   └── queries.go       #   Insert/query helper functions.
│   └── llm/                 # Model-calling client + interface + fake.
│       ├── llm.go           #   LLM interface, Request/Response/Message/Tool/ToolCall/TokenUsage.
│       ├── openai.go        #   Real implementation: wraps sashabaranov/go-openai.
│       └── fake.go          #   In-memory fake implementing LLM, for unit tests.
├── docs/
│   └── adr-phase-1-foundation.md   # ← this file.
├── go.mod
├── go.sum
└── .gitignore
```

### Justification per package

| Package | Rationale |
|---------|-----------|
| `cmd/harness/` | Standard Go convention for CLI entry point. Phase 1 produces a stub only, but establishing the directory now avoids a directory-rename later when Phase 4 wires config/bootstrap. |
| `internal/store/` | SQLite access owns its own connection lifecycle and schema — grouping it as one package with no sub-packages keeps the boundary clean. The three struct types live here alongside their helpers since they have no consumers outside store (all external use goes through the exported function API). |
| `internal/llm/` | The interface, its real implementation, and its test fake all belong together — Go convention places interface where consumed (the harness turn loop in Phase 3) but the *definition* goes here because the client is the natural owner of the types. Phase 3's loop code imports `internal/llm` for the interface, not the other way around. |

No `pkg/` directory: this is a single-binary tool, not a library for external consumers. No empty "seam" directories for Phase 2+ — later phases add their own internal packages (`internal/tools/`, `internal/config/`, `internal/loop/`) when they are actually introduced.

---

## 2. SQLite Storage

### 2.1 Driver: `modernc.org/sqlite` (pure Go, no cgo)

- **Chosen:** `modernc.org/sqlite` (import: `modernc.org/sqlite` — registers driver name `"sqlite"`).
- **Rejected alternative:** `github.com/mattn/go-sqlite3` — requires cgo (gcc, cross-compile issues). The devbox VM has no unusual toolchain constraints, but avoiding cgo eliminates an entire class of build failures and keeps `go build` fast.
- **Go SQL wrapper:** Raw `database/sql` (no `sqlx`). With only three tables and straightforward CRUD, the struct-scanning convenience of sqlx doesn't offset the extra dependency. The builder may add sqlx if they find manual `Scan` too tedious — flag it in the phase log if they do.

### 2.2 DSN format

Plain file path. modernc accepts:
```go
db, err := sql.Open("sqlite", filepath.Join(rootDir, "agent-harness.db"))
```
No URI prefix needed. If `:memory:` is wanted for tests, pass `"file::memory:?mode=memory&cache=shared"` (modernc supports SQLite URI-style DSNs).

### 2.3 Connection setup

Executed immediately after `sql.Open` (before any query):
```sql
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
```
- `journal_mode=WAL` per spec §4 — allows concurrent reads during continuous writes.
- `foreign_keys=ON` — SQLite defaults to OFF; must be set per-connection. The schema has `REFERENCES` clauses that should be enforced.

`sql.Open` itself is lazy (does not open a connection) — the caller or the `Open()` function must call `db.Ping()` to verify the database is reachable.

### 2.4 Schema creation (idempotent)

Embed `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS` statements in a Go string literal (or `//go:embed` a `.sql` file, though embedding a separate file adds a file to track with no real benefit for a 20-line schema). Run them in a single `db.Exec` call (SQLite allows multiple semicolon-separated statements) inside the `Open()` function.

The schema MUST be defined in the order: `sessions` → `files` → `events` → indexes — because `files` and `events` reference `sessions(id)`, and `events` references `files(id)`.

### 2.5 Go struct types

#### Session

```go
type Session struct {
    ID               int64   // PRIMARY KEY, auto-increment
    Project          string  // project name/identifier
    Phase            int     // phase number
    Mode             string  // architect|builder|tester|forensic|librarian
    ModelName        string
    BaseURL          string
    ContextMaxTokens int
    ResumeCount      int     // defaults to 0
    StartedAt        string  // ISO 8601 (RFC3339), set at creation
    EndedAt          *string // nullable, set when session terminates
    Status           string  // running|done|halted|error
}
```

#### File

```go
type File struct {
    ID           int64   // PRIMARY KEY, auto-increment
    SessionID    int64   // FK → sessions(id)
    Path         string  // NOT NULL
    ContentHash  *string // nullable SHA-256 hex; nil before first write
    LastEventID  *int64  // nullable FK → events(id); nil before first write
    WriteCount   int     // defaults to 0
    // UNIQUE(session_id, path) enforced at DB level
}
```

#### Event

```go
type Event struct {
    ID         int64   // PRIMARY KEY, auto-increment
    SessionID  int64   // FK → sessions(id)
    TurnIndex  *int    // nullable (events may occur pre-turn); 0-based
    EventType  string  // model_call|tool_call|tool_result|text|halt
    ToolName   *string // nullable; edit_file|bash_exec|read_file|create_file|list_dir|write_log
    FileID     *int64  // nullable FK → files(id); relevant tool's target file
    ArgsJSON   *string // nullable; raw JSON of tool arguments
    ResultJSON *string // nullable; raw JSON of tool result
    TokensUsed *int    // nullable; populated for model_call events
    CreatedAt  string  // ISO 8601 (RFC3339)
}
```

**Design notes on nullability:**
- `*string` for nullable TEXT columns, `*int64`/`*int` for nullable INTEGER columns — Go 1.13+ `database/sql.Scan` handles pointer types natively, so `sql.NullString` wrappers are unnecessary.
- `StartedAt`/`CreatedAt` are stored as ISO 8601 strings (not `time.Time`) to keep the SQL round-trip explicit and avoid driver-dependent timezone conversion behavior. The caller formats with `time.Now().UTC().Format(time.RFC3339)` before inserting.
- `ContentHash` is SHA-256 hex-encoded (64-character lowercase hex string). Algorithm choice is `crypto/sha256`. This is the loop-detection substrate (§7): unchanged hash after an edit-tool write is a strong loop signal.

### 2.6 CRUD helper functions (Store API)

```go
// Open opens (or creates) the SQLite database at path, runs schema migration,
// sets WAL + foreign_keys PRAGMAs, and returns a Store ready for queries.
func Open(ctx context.Context, path string) (*Store, error)

// Close shuts down the database connection pool.
func (s *Store) Close() error

// InsertSession creates a new session row. Returns the auto-increment ID.
func (s *Store) InsertSession(ctx context.Context, sess Session) (int64, error)

// UpdateSession updates an existing session (status, ended_at, resume_count).
func (s *Store) UpdateSession(ctx context.Context, sess Session) error

// InsertEvent logs a new event. Returns the auto-increment ID.
func (s *Store) InsertEvent(ctx context.Context, evt Event) (int64, error)

// UpsertFile creates or updates a file row keyed on (session_id, path).
// On insert: write_count=0, content_hash=nil. On update (write): increments write_count,
// updates content_hash and last_event_id. See spec §7 — write_count is the loop-detection signal.
func (s *Store) UpsertFile(ctx context.Context, f File) (int64, error)

// FileWriteCount returns the write_count for a given session+path.
// Used by Phase 3's hardcoded halt check (§7.1).
func (s *Store) FileWriteCount(ctx context.Context, sessionID int64, path string) (int, error)

// EventsBySession returns all events for a session, ordered by (turn_index, id).
// Used by Phase 3's delta halt check (§7.2) and session-reuse summary (§9).
func (s *Store) EventsBySession(ctx context.Context, sessionID int64) ([]Event, error)
```

The exact SQL for `UpsertFile` uses `INSERT ... ON CONFLICT(session_id, path) DO UPDATE SET ...` (SQLite's UPSERT, available since 3.24.0).

---

## 3. LLM Interface and OpenAI-Compatible Client

### 3.1 Core types

```go
// LLM is the interface for calling an OpenAI-compatible chat completion API.
// The only method is Call. Streaming is deferred to v2.
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
    Text      string     // The model's text reply (empty if only tool_calls)
    ToolCalls []ToolCall // Parsed tool calls, if any
    Usage     TokenUsage
}

type Message struct {
    Role       string     // "system" | "user" | "assistant" | "tool"
    Content    string
    ToolCallID string     // non-empty only when Role == "tool"
    ToolCalls  []ToolCall // non-empty only when Role == "assistant"
}

type ToolDef struct {
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Parameters  any    `json:"parameters"` // JSON-serializable schema
}

type ToolCall struct {
    ID       string          `json:"id"`
    Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // raw JSON string
}

type TokenUsage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}
```

**Why `Message.ToolCalls` is `[]ToolCall` rather than a separate struct:** this mirrors the OpenAI wire format where assistant messages carry `tool_calls` directly. A single `Message` type suffices for all four roles — the caller sets only the fields relevant to the current role.

### 3.2 Client library: `sashabaranov/go-openai`

- **Chosen:** `github.com/sashabaranov/go-openai`
- **Rationale:** The most widely used Go OpenAI client (7K+ GitHub stars), pure Go (no cgo), actively maintained, directly maps to the OpenAI-compatible chat completions wire format. The devbox can reach OpenAI-compatible endpoints at `https://api.metisai.ir/v1` and `http://localhost:7890/v1` (local Qwen) — both served by the same wire protocol.
- **Integration approach:** The go-openai library is used *inside* `openai.go` only. The public API surface (the `LLM` interface and its types) remains independent of go-openai. Translation between our `Request`/`Response` types and go-openai's `ChatCompletionRequest`/`ChatCompletionResponse` happens entirely within the `Call` implementation. This keeps the interface swappable and the fake implementation free of any go-openai dependency.

### 3.3 Error classification

The `Call` method returns errors that carry a classification category, allowing Phase 3's retry/backoff logic (§8) to distinguish error types without inspecting provider-specific strings or status codes.

```go
type ErrorCategory int

const (
    ErrCategoryOther     ErrorCategory = iota // uncategorized
    ErrCategoryTimeout                         // context deadline or HTTP timeout
    ErrCategoryRateLimit                       // HTTP 429
    ErrCategoryQuota                           // insufficient_quota / HTTP 403/429 with quota body
    ErrCategoryMalformed                       // 200 OK but response body is garbled/empty
)

// LLMError wraps an error with a category and optional metadata.
type LLMError struct {
    Err        error
    Category   ErrorCategory
    RetryAfter time.Duration // populated only for ErrCategoryRateLimit (from Retry-After header)
    StatusCode int           // HTTP status code, 0 if not an HTTP error
}

func (e *LLMError) Error() string {
    return fmt.Sprintf("llm: %s (category=%d, http=%d)", e.Err.Error(), e.Category, e.StatusCode)
}

func (e *LLMError) Unwrap() error { return e.Err }
```

**Usage by Phase 3's retry logic (not implemented here, but designed for):**

```go
var llmErr *LLMError
if errors.As(err, &llmErr) {
    switch llmErr.Category {
    case ErrCategoryTimeout:
        // backoff, retry
    case ErrCategoryRateLimit:
        // time.Sleep(llmErr.RetryAfter) then retry
    case ErrCategoryQuota:
        // halt, require user intervention
    case ErrCategoryMalformed:
        // one immediate retry, then halt
    }
}
```

Go 1.26's `errors.AsType[T]` could also be used, but `errors.As` with a pointer is the canonical pattern and works across all Go 1.13+ versions equally.

**How each category is detected inside `openai.go`:**

| Category | Detection |
|----------|-----------|
| `ErrCategoryTimeout` | `ctx.Err() == context.DeadlineExceeded` or request's context deadline is exceeded. The client passes the caller's `ctx` directly to go-openai's `CreateChatCompletion` — if the context has a deadline, go-openai respects it. |
| `ErrCategoryRateLimit` | Check go-openai's response error for HTTP status 429. If present, extract `Retry-After` header value (parsed as seconds integer or HTTP-date). |
| `ErrCategoryQuota` | Check go-openai's error body for `insufficient_quota` or `exceeded_quota` strings in the code/message field. Also HTTP 403 with billing-related body. |
| `ErrCategoryMalformed` | Successful HTTP (200) but `Response.ToolCalls` contains invalid JSON in the `arguments` field, or `choices` array is empty or missing required fields. |
| `ErrCategoryOther` | Any error that doesn't match above (network DNS failure, TLS error, unexpected status code). |

**Important:** The go-openai library returns its own error types (`openai.APIError`, `openai.RequestError`). The `openai.go` implementation must inspect these and translate to `LLMError` — it must NOT let raw go-openai errors propagate through the `LLM` interface unclassified.

### 3.4 Fake implementation (for testing)

```go
// Fake is an in-memory LLM implementation for tests.
// It returns a fixed Response and no error unless configured otherwise.
type Fake struct {
    Response Response
    Err      error
}

func (f *Fake) Call(ctx context.Context, req Request) (Response, error) {
    // If configured with a Responder function, delegate to it for per-call logic.
    if f.responder != nil {
        return f.responder(ctx, req)
    }
    return f.Response, f.Err
}
```

The `Fake` should support:
1. A fixed response (simple case — most tests).
2. A `Responder` function `func(ctx context.Context, req Request) (Response, error)` for tests that need per-call inspection or conditional responses.
3. Default zero-value behavior: returns empty `Response` with no error — safe for tests that don't care about the response content.

---

## 4. Configuration (deferred to Phase 4)

No config parsing or `go:embed` in this phase. The `BaseURL` and `Model` fields in `Request` are populated by the caller with string literals or env vars for now. Phase 4 will introduce YAML-frontmatter parsing and `.aa/` bootstrapping.

---

## 5. Testing Strategy for Phase 1

The builder's test suite should cover:

1. **Store: schema correctness** — Open a `:memory:` DB, verify all three tables exist (query `sqlite_master`), verify WAL mode is active (`PRAGMA journal_mode` returns `wal`), verify foreign_keys is ON.
2. **Store: idempotent schema** — Open a `:memory:` DB twice in sequence; second open does not error.
3. **Store: round-trip** — Insert a session, read it back, fields match. Same for events and files (with FK constraints).
4. **Store: UPSERT behavior** — Insert a file, upsert it again with incremented write_count, verify count increased and content_hash updated.
5. **LLM: interface compliance** — The `Fake` satisfies `LLM` at compile time (`var _ LLM = (*Fake)(nil)`).
6. **LLM: error classification** — Use the `Fake` to verify that error-category inspection via `errors.As` works as designed, even though the fake never produces real LLMErrors. (The real error classification logic in `openai.go` can't be meaningfully tested without a real HTTP server — Phase 2 may add an HTTP test helper.)
7. **LLM: real client compiles** — `go build ./internal/llm/...` succeeds. The real client does not need to make a successful network call in this phase's tests.

The fake `LLM` implementation from this phase will be the primary test dependency for Phase 2 (tools) and Phase 3 (turn loop) — it must be reliable and fully deterministic.

---

## 6. Open Decisions / Items for Builder to Flag

1. **sqlx vs raw database/sql:** If the manual `rows.Scan` boilerplate for three struct types becomes too tedious during implementation, the builder may switch to `jmoiron/sqlx` — but must note the decision in `phase-1.log` and update this ADR's dependency section.
2. **modernc DSN for in-memory tests:** If `":memory:"` does not work correctly with concurrent access patterns, use `"file::memory:?mode=memory&cache=shared"` instead.
3. **go-openai version pinning:** The builder should `go get` the latest tagged version of `github.com/sashabaranov/go-openai` at the time of implementation. If any breaking API changes exist since writing, flag them.
4. **`ContentHash` algorithm:** SHA-256 specified. If performance of hashing large files on each edit becomes a concern in Phase 2+, the builder should flag it — but do not optimize prematurely.
5. **`cmd/harness/main.go` minimal content:** The builder should create a minimal main that opens a DB, creates a (fake) LLM client, logs a startup event, and exits cleanly. This demonstrates the foundation end-to-end without needing tools or a loop.
6. **`.gitignore` entry:** The builder should add `*.db` (or `agent-harness.db`) to `.gitignore` per spec §15.

---

## 7. Fix Cycle 1 — Retry-After Capture and Error Category Granularity

**Date:** 2026-07-05
**Trigger:** Tester E2E pass (42/42) surfaced two gaps in `openai.go` that §8 (Phase 3 retry/backoff) depends on.

### 7.1 Decision 1 — `Retry-After` Header Capture

#### Problem
`parseRetryAfter` returns 0 unconditionally. `go-openai`'s `APIError` type does not expose raw HTTP response headers — they are consumed during `handleErrorResp` and discarded. No path exists to read a `Retry-After` header from within `classifyError`.

#### Options Considered

| Option | Description | Rejection reason |
|--------|-------------|------------------|
| (a) Wrapping `go-openai`'s HTTP client with a custom `http.RoundTripper` | Intercept the raw `*http.Response` before go-openai consumes it, read `Retry-After` on 429, store it in a thread-safe capture object accessible to `classifyError`. | **Chosen** |
| (b) Fork or patch go-openai to expose headers on APIError | Would pin the project to a fork — unacceptable maintenance burden for a single header read. | Rejected — creates a fork dependency for one field. |
| (c) Parse `Retry-After` from the error message body | Some providers include `Retry-After` in the JSON body. Non-standard, unreliable. | Rejected — not all providers include it, and this would miss the standard header. |

#### Chosen Approach: Custom `http.RoundTripper` (Option a)

Add two new internal types to `openai.go`:

```go
// retryAfterCapture stores the Retry-After duration from the most recent HTTP
// 429 response. Thread-safe via mutex. Reset after each read.
type retryAfterCapture struct {
    mu         sync.Mutex
    retryAfter time.Duration
}

func (c *retryAfterCapture) set(d time.Duration) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.retryAfter = d
}

func (c *retryAfterCapture) getAndReset() time.Duration {
    c.mu.Lock()
    defer c.mu.Unlock()
    d := c.retryAfter
    c.retryAfter = 0
    return d
}

// captureTransport wraps an http.RoundTripper to capture Retry-After headers
// from 429 responses before go-openai consumes the response.
type captureTransport struct {
    inner   http.RoundTripper
    capture *retryAfterCapture
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    resp, err := t.inner.RoundTrip(req)
    if err != nil {
        return resp, err
    }
    if resp.StatusCode == 429 {
        ra := resp.Header.Get("Retry-After")
        if ra != "" {
            d, parseErr := parseHTTPRetryAfter(ra)
            if parseErr == nil {
                t.capture.set(d)
            }
        }
    }
    return resp, err
}
```

Add a standalone `parseHTTPRetryAfter` function (distinct from the now-obsolete `parseRetryAfter` on `*goopenai.APIError`):

```go
// parseHTTPRetryAfter parses a Retry-After header value.
// Supports both integer-seconds and HTTP-date (RFC1123) formats.
func parseHTTPRetryAfter(ra string) (time.Duration, error) {
    // Try integer seconds first (most common).
    if secs, err := strconv.Atoi(ra); err == nil {
        if secs < 0 {
            return 0, fmt.Errorf("negative retry-after: %d", secs)
        }
        return time.Duration(secs) * time.Second, nil
    }
    // Try HTTP-date format.
    if t, err := time.Parse(time.RFC1123, ra); err == nil {
        d := time.Until(t)
        if d < 0 {
            return 0, nil
        }
        return d, nil
    }
    return 0, fmt.Errorf("unparseable retry-after: %q", ra)
}
```

**Changes to `OpenAIClient`:**

```go
type OpenAIClient struct {
    capture *retryAfterCapture
}

func NewOpenAIClient() *OpenAIClient {
    return &OpenAIClient{
        capture: &retryAfterCapture{},
    }
}
```

**Changes to `Call`:**

Before creating the go-openai client, set `config.HTTPClient` to an `http.Client` whose `Transport` is the capture wrapper:

```go
config.HTTPClient = &http.Client{
    Transport: &captureTransport{
        inner:   http.DefaultTransport,
        capture: c.capture,
    },
}
```

This ensures every HTTP request made by this `Call` invocation passes through the capture wrapper.

**Changes to `classifyError`:**

Make `classifyError` a method on `*OpenAIClient` so it can access the capture:

```go
func (c *OpenAIClient) classifyError(ctx context.Context, err error) error {
```

Inside the 429 case, replace the call to `parseRetryAfter(apiErr)` with:

```go
retryAfter := c.capture.getAndReset()
```

If `getAndReset` returns 0 (no header was captured, or parsing failed), the caller (Phase 3) applies its own default backoff — same behavior as the current stub.

**No new `LLMError` fields are needed.** The existing `RetryAfter time.Duration` field on `LLMError` already carries the parsed value. The capture mechanism is purely internal plumbing.

### 7.2 Decision 2 — Error Category Granularity

#### Problem
`ErrCategoryOther` is an undifferentiated catch-all receiving:
- HTTP 401 (bad API key) — a permanent config error, retry is pointless
- HTTP 403 (forbidden, non-quota) — same class as auth
- Network/DNS/TLS failures (`RequestError`) — may be transient
- Unexpected status codes — may be transient

Phase 3's retry logic (§8) cannot distinguish between "stop, fix your config" and "maybe retry" from the category alone.

#### Chosen Approach: Split `ErrCategoryOther` into `ErrCategoryAuth` + `ErrCategoryUnknown`

**New enum values in `internal/llm/llm.go`:**

```go
const (
    ErrCategoryUnknown    ErrorCategory = iota // was ErrCategoryOther — unclassified/unexpected
    ErrCategoryTimeout                          // context deadline or HTTP timeout
    ErrCategoryRateLimit                        // HTTP 429
    ErrCategoryQuota                            // insufficient_quota / HTTP 403 or 429 with quota body
    ErrCategoryAuth                             // HTTP 401 (bad API key), 402/403 non-quota
    ErrCategoryMalformed                        // 200 OK but response body is garbled/empty
)
```

**Impact on `classifyError` logic in `openai.go`:**

Replace the current grouped 400/401/402/403 case with per-status-case logic:

| Condition | New Category | Rationale |
|---|---|---|
| HTTP 401 (any body) | `ErrCategoryAuth` | Unauthorized — bad API key, expired token. Never resolves by retrying. |
| HTTP 402 — body matches quota keywords | `ErrCategoryQuota` | Payment required with quota language. |
| HTTP 402 — body does NOT match quota keywords | `ErrCategoryAuth` | Payment required / account frozen — user must intervene. |
| HTTP 403 — body matches quota keywords (`quota`/`insufficient_quota`/`exceeded`) | `ErrCategoryQuota` | Same as existing quota logic. |
| HTTP 403 — body does NOT match quota keywords | `ErrCategoryAuth` | Forbidden — permission issue, not a quota problem. User must fix config. |
| HTTP 400 — body matches quota keywords | `ErrCategoryQuota` | Same as existing quota logic. |
| HTTP 400 — body does NOT match quota keywords | `ErrCategoryUnknown` | Bad request — could be transient or permanent, retry with backoff. |
| HTTP 429 — body matches quota keywords | `ErrCategoryQuota` | Same as existing quota logic. |
| HTTP 429 — other body | `ErrCategoryRateLimit` | Same as existing, plus `RetryAfter` from capture transport. |
| Other `goopenai.APIError` status codes (5xx, etc.) | `ErrCategoryUnknown` | Server errors — may be transient, retry with backoff. |
| `goopenai.RequestError` (network/DNS/TLS) | `ErrCategoryUnknown` | Transient — retry with backoff. |
| Fallback | `ErrCategoryUnknown` | Uncategorized — retry with backoff. |

**Extension to spec §8 retry table:**

The spec's retry/backoff table (currently in `agent-harness_spec.md` §8) gains two new rows:

| Error class | Category | Behavior |
|---|---|---|
| Auth failure (401, 402/403 non-quota) | `ErrCategoryAuth` | **Not auto-retried.** Halt immediately, log the error with `StatusCode`, exit with non-zero status. User must fix API key / provider config before re-running. Distinguishable from Quota via `StatusCode` (401/402 vs 403/429) for log clarity. |
| Unknown/server error (5xx, network/DNS/TLS, unexpected status, `RequestError`) | `ErrCategoryUnknown` | Bounded exponential backoff with jitter, capped at 5 minutes total, then log + trigger session-reuse sequence + exit. Same timeout envelope as `ErrCategoryTimeout`. First-party code should log the raw error for diagnosis. |

**No new `LLMError` fields.** The existing `StatusCode` field lets Phase 3 distinguish between a 401 auth error and a 403 auth error. The `Err` chain carries the original go-openai error for detailed logging.

### 7.3 Files Requiring Changes

| File | Changes |
|---|---|
| `internal/llm/llm.go` | Replace `ErrCategoryOther` -> `ErrCategoryUnknown`, add `ErrCategoryAuth` constant. Update comment. |
| `internal/llm/openai.go` | Add `retryAfterCapture`, `captureTransport`, `parseHTTPRetryAfter`. Add `capture` field to `OpenAIClient`. Update `Call` to set `config.HTTPClient`. Make `classifyError` a method. Update switch logic per mapping table above. Remove `parseRetryAfter(*goopenai.APIError)`. |
| `internal/llm/llm_test.go` | Update `TestLLMError_Categories`: change "other" test case to use `ErrCategoryUnknown` and add a new "auth" test case with `ErrCategoryAuth`. |
| `e2e/phase1_test.go` | Update `TestE2E_OpenAIClient_BadAuth`: change expected category from `ErrCategoryOther` to `ErrCategoryAuth`. Optionally add a new E2E test for 429 with `Retry-After` header. |

### 7.4 Constraints and Risks

1. **The capture transport wraps `http.DefaultTransport`.** If the environment uses a non-default transport (e.g. for proxy support or custom TLS), `NewOpenAIClient` should accept an optional `http.RoundTripper` parameter. This is not needed for Phase 1/2 but is a seam to leave open. For now, hardcode `http.DefaultTransport` — Phase 4's config layer can thread in a custom transport.

2. **The capture mutex exists for correctness but is not exercised** in the current serial-turn architecture. It costs nothing and prevents a race if Phase 3+ ever adds concurrent LLM calls (which it shouldn't, per spec "parallel tool calls deferred to v2"). Adding it now avoids a silent-race introduction later.

3. **`Retry-After` HTTP-date format is supported but rarely used in practice.** Providers overwhelmingly send integer seconds. The RFC1123 parser is a correctness measure, not a hot path.

4. **The enum rename (`ErrCategoryOther` -> `ErrCategoryUnknown`) is a breaking change** for any code that references the old name. Only internal tests do so — no external consumers, since the `llm` package is in `internal/`. The builder must update all references.

5. **The retry behavior for `ErrCategoryAuth` is "halt immediately".** This means a typo in the API key causes a hard stop with no retry cost. The user must fix the key and re-run. This is the correct behavior: retrying an auth failure will never succeed (unlike a transient network error).
