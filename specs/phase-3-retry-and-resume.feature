# SPEC: Phase 3 — Retry/Backoff, Session-Reuse, Retry-Halt Interaction
# BUDGET: medium 5-10K
# SCOPE: internal/loop/, internal/store/queries.go
# STATUS: approved

Feature: Retry/Backoff, Session-Reuse, and Retry-Halt Interaction

  As a harness operator
  I want the turn loop to retry transient LLM errors with appropriate backoff,
  reuse the same session_id with a fresh summary when transient halts occur,
  and ensure retries do not interact with halt-detection counters
  So that robustness is built into the agent loop without false-positive halts.

  Background:
    Given a TurnLoop is configured with:
      | param               | value                              |
      | AgentConfig.Name     | "builder"                          |
      | AgentConfig.ModelName| "fake-model"                       |
      | AgentConfig.BaseURL  | "http://fake.test/v1"              |
      | AgentConfig.ContextMaxTokens | 32000                     |
      | LogPath              | "/tmp/phase-test.log"              |
      | ProjectRoot          | "/tmp/test-project"                |
    And the Store is opened on an in-memory SQLite database
    And the Registry contains all 6 default tools
    And the RetryPolicy defaults are active:
      | param               | value |
      | initialBackoff      | 1s    |
      | backoffMultiplier   | 2.0   |
      | maxBackoffDuration  | 5min  |
      | maxAttemptsUnknown  | 3     |
    And the Fake LLM returns a text response "Task complete." for any successful call

  # ---------------------------------------------------------------------------
  # Area 4: Retry/Backoff — Per Error Category
  # ---------------------------------------------------------------------------

  Scenario: Timeout error retries with exponential backoff then succeeds
    Given the Fake LLM returns an LLMError of category ErrCategoryTimeout on the first 2 calls
    And the Fake LLM returns a text response "Done." on the 3rd call
    And the Fake LLM records call timestamps for backoff measurement
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And exactly 3 LLM calls were made
    And the delay between the 1st and 2nd call is approximately 1 second (the initial backoff)
    And the delay between the 2nd and 3rd call is approximately 2 seconds (doubled backoff)
    And exactly 0 halt events are logged (no timeout halt — retry succeeded)

  Scenario: Timeout backoff exhaustion triggers session-reuse halt
    Given the Fake LLM returns an LLMError of category ErrCategoryTimeout on every call
    And the backoff cap is reached after 3 retries (cumulative wait exceeds 5-minute cap)
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltTimeout
    And the halt message contains "backoff exhausted"
    And the session status is "halted"
    And the session was set to "running" again after resume (resume_count incremented)

  Scenario: RateLimit error with RetryAfter header honors the header duration
    Given the Fake LLM returns an LLMError of category ErrCategoryRateLimit on the first call
    And the LLMError has RetryAfter = 30 seconds
    And the Fake LLM returns a text response "Done." on the 2nd call
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And exactly 2 LLM calls were made
    And the delay between calls is approximately 30 seconds (from RetryAfter header, not exponential backoff)

  Scenario: RateLimit error without RetryAfter falls back to exponential backoff
    Given the Fake LLM returns an LLMError of category ErrCategoryRateLimit on the first call
    And the LLMError has RetryAfter = 0 (no header captured)
    And the Fake LLM returns a text response "Done." on the 2nd call
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And exactly 2 LLM calls were made
    And the delay between calls is approximately 1 second (exponential backoff base, not RetryAfter)

  Scenario: Quota error halts immediately with no retry
    Given the Fake LLM returns an LLMError of category ErrCategoryQuota on the first call
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltQuota
    And exactly 1 LLM call was made (no retry)
    And the halt message contains "quota"
    And the session status is "error"
    And no tool_call events are logged (no tool dispatch occurred)

  Scenario: Malformed response retries once then halts on second occurrence
    Given the Fake LLM returns an LLMError of category ErrCategoryMalformed on the first call
    And the Fake LLM returns an LLMError of category ErrCategoryMalformed on the second call (immediate retry)
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltMalformed
    And exactly 2 LLM calls were made (original + one immediate retry)
    And there is no backoff delay between the 1st and 2nd call (malformed retries are immediate)
    And the halt message contains "repeated malformed response"
    And the session resumes with resume_count incremented

  Scenario: Malformed response retry succeeds on second attempt
    Given the Fake LLM returns an LLMError of category ErrCategoryMalformed on the first call
    And the Fake LLM returns a text response "Done." on the second call
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And exactly 2 LLM calls were made
    And no halt events are logged
    And no write_count increments occurred (no tool was dispatched on the first malformed call)

  Scenario: Auth error halts immediately with no retry
    Given the Fake LLM returns an LLMError of category ErrCategoryAuth with StatusCode=401 on the first call
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltAuth
    And exactly 1 LLM call was made (no retry)
    And the halt message contains "auth failure"
    And the halt message contains "401"
    And the session status is "error"

  Scenario: Unknown error retries up to 3 times then halts
    Given the Fake LLM returns an LLMError of category ErrCategoryUnknown on the first 3 calls
    And the Fake LLM returns a response on the 4th call
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltUnknown
    And exactly 3 LLM calls were made (reached maxAttemptsUnknown)
    And the halt message contains "3 consecutive unknown errors"
    And the session resumes with resume_count incremented

  Scenario: Unknown error recovers before max attempts
    Given the Fake LLM returns a sequence of errors:
      | call | category             |
      | 1    | ErrCategoryUnknown   |
      | 2    | ErrCategoryUnknown   |
      | 3    | nil (success)        |
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And exactly 3 LLM calls were made
    And the session completes normally

  # ---------------------------------------------------------------------------
  # Area 5: Session-Reuse
  # ---------------------------------------------------------------------------

  Scenario: Timeout halt increments resume_count and uses the same session_id
    Given the Fake LLM returns ErrCategoryTimeout on all calls
    And the session initially has resume_count = 0
    And the session_id is, say, 42
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltTimeout
    And the session_id is still 42 (the same DB row is reused, not a new row created)
    And the session's resume_count is 1 (incremented from the initial 0)
    And the session DB row shows that started_at was updated and ended_at was cleared
    And the session's status is "running" (resumeSession set it back to "running" for the next attempt)

  Scenario: Resume message history is a templated summary, not a replay
    Given a prior Run() call completed 3 successful turns with:
      | event_type | tool_name  | args_json               | result_json          |
      | tool_call  | edit_file  | {"path":"a.go","old_str":"x","new_str":"y"} |                      |
      | tool_result | edit_file |                          | {"path":"a.go","matches_found":1,"content_hash":"abc"} |
      | tool_call  | read_file  | {"path":"b.go"}          |                      |
      | tool_result | read_file |                          | {"path":"b.go","content":"pkg b","line_count":1}       |
    And those events and the files table are stored in the DB with session_id=42
    And the same TurnLoop is now configured with a Fake LLM that returns ErrCategoryMalformed on every call
    When TurnLoop.Run() is called
    Then the TurnLoop stores a resume summary generated from the prior session events
    And the resume summary contains:
      | text                                                          |
      | "Previous session #42 halted."                                |
      | "Reason: repeated malformed response after retry"             |
      | "Files touched:"                                              |
      | "  - a.go" (with write count)                                |
      | "  - b.go" (with write count)                                |
    And the resume summary does NOT contain the original user kickoff prompt text
    And the resume summary does NOT contain raw tool arguments ({"path":"a.go",...})
    And the resume summary uses the defined text/template (not a raw event replay)

  Scenario: Resume summary reflects actual session data (files touched, last failure)
    Given the first session attempt made 3 edits to "main.go" and 1 edit to "utils.go"
    And the first session halted due to timeout at turn 4
    When TurnLoop.Run() is called
    Then the resume summary includes:
      - "main.go (3 writes)"
      - "utils.go (1 writes)"
    And the resume summary includes a line about the timeout halt
    And the resume summary includes recent activity lines from events

  # ---------------------------------------------------------------------------
  # Area 6: Retry-Halt Interaction
  # ---------------------------------------------------------------------------

  Scenario: Malformed retry that succeeds does not count against halt-detection write counters
    Given the Fake LLM returns ErrCategoryMalformed on the first call
    And the Fake LLM returns tool calls on the second call:
      | call | tool_calls                                         | text_response |
      | 2    | edit_file(path="a.go", old_str="x", new_str="y") |               |
      | 3    |                                                    | "Done."       |
    And "a.go" has write_count=0 and no prior writes
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And exactly 1 edit_file on "a.go" was dispatched (the malformed retry produced no tool call)
    And the write_count for "a.go" is 1 (only the successful tool call counted)
    And no halt events are logged
    And the malformed error did not increment the write_count counter for "a.go"
    And the malformed error did not increment any file's write_count
