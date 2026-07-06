# SPEC: Phase 3 — Loop Basics, Halt Detection, Token Budget
# BUDGET: medium 5-10K
# SCOPE: internal/loop/, internal/tools/edit_file.go, internal/tools/create_file.go
# STATUS: approved

Feature: Turn Loop Basics, Halt Detection, and Token Budget

  As a harness operator
  I want the turn loop to correctly sequence model calls and tool dispatch,
  detect looping behavior via hardcoded and delta signals,
  and stop when cumulative token usage approaches the context limit
  So that agent sessions are productive, loop-free, and bounded.

  Background:
    Given a TurnLoop is configured with:
      | param               | value                              |
      | AgentConfig.Name     | "builder"                          |
      | AgentConfig.ModelName| "fake-model"                       |
      | AgentConfig.BaseURL  | "http://fake.test/v1"              |
      | AgentConfig.ContextMaxTokens | 32768                    |
      | AgentConfig.Tools    | "read_file,edit_file,create_file,bash_exec,list_dir,write_log" |
      | LogPath              | "/tmp/phase-test.log"              |
      | ProjectRoot          | "/tmp/test-project"                |
    And the Store is opened on an in-memory SQLite database
    And the Registry contains all 6 default tools
    And the Fake LLM returns no error by default

  # ---------------------------------------------------------------------------
  # Area 1: Turn Loop Basics
  # ---------------------------------------------------------------------------

  Scenario: Session terminates after one model response with no tool calls
    Given the Fake LLM is configured to return a text response "Task complete." with no tool calls
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And the session status is "done"
    And exactly 1 model_call event is logged for the session
    And exactly 0 tool_call events are logged
    And the message history contains the system prompt, the user prompt, and no assistant tool_calls

  Scenario: Session with tool calls loops until model stops calling tools
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                     | text_response |
      | 1    | edit_file(path="a.go", old_str="foo", new_str="bar") |               |
      | 2    | read_file(path="a.go")                         |               |
      | 3    |                                                | "Done."       |
    And the Store has a file row for "a.go" with write_count=0 and content_hash=nil
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And the session has exactly 3 turns
    And turn 0 has 1 model_call event and 1 tool_call event and 1 tool_result event
    And turn 1 has 1 model_call event and 1 tool_call event and 1 tool_result event
    And turn 2 has 1 model_call event and 0 tool_call events
    And the message history contains 3 assistant messages and 2 tool messages
    And the final message is the model's text "Done."

  Scenario: Tool results are appended to message history before the next model call
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                     | text_response |
      | 1    | edit_file(path="a.go", old_str="foo", new_str="bar") |               |
      | 2    | read_file(path="a.go")                         |               |
      | 3    |                                                | "Done."       |
    When TurnLoop.Run() is called
    Then the second LLM call receives a message history that includes the tool result from the first edit_file call
    And the third LLM call receives a message history that includes the tool result from the read_file call
    And the edit_file tool was executed with old_str="foo", new_str="bar"
    And the read_file tool was executed on path="a.go"

  Scenario: Multiple tool calls in one turn are processed serially
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                                                                  | text_response |
      | 1    | edit_file(path="a.go", old_str="x", new_str="y"), create_file(path="b.go", content="package b") |               |
      | 2    |                                                                                             | "Done."       |
    When TurnLoop.Run() is called
    Then the session has exactly 2 turns
    And turn 0 has 1 model_call event, 2 tool_call events, and 2 tool_result events
    And the edit_file tool was executed before the create_file tool
    And both tool results appear in the message history sent to the model for turn 1

  Scenario: Tool result errors are surfaced to the model as clear messages
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                                   | text_response |
      | 1    | edit_file(path="missing.go", old_str="foo", new_str="bar")   |               |
      | 2    |                                                              | "I see the error, let me fix it." |
    And the file "missing.go" does not exist on disk
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And the model receives a tool result message containing "ERROR: edit_file:"
    And the model receives a tool result message containing "zero matches"
    And the session has exactly 2 turns

  # ---------------------------------------------------------------------------
  # Area 2: Halt Detection — Hardcoded
  # ---------------------------------------------------------------------------

  Scenario: Write-count threshold triggers immediate halt after the Nth write
    Given the Fake LLM is configured with a Responder that returns 1 edit_file tool call per turn for 7 consecutive turns
    And each edit_file call targets "looping.go" with old_str="a" new_str="b"
    And the hardcoded maximum file writes is 5
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltHardcoded
    And the halt message contains "looping.go"
    And the halt message contains "edited 5 times"
    And exactly 5 tool_call events are logged for "edit_file" on "looping.go"
    And no LLM call is made after the 5th edit_file result (no wasted API call)
    And the session status is "halted"

  Scenario: Content-hash unchanged after edit triggers halt even below write-count threshold
    Given the Fake LLM is configured with a Responder that returns 3 consecutive edit_file calls on "stale.go"
    And each edit_file result has the same ContentHash value (content did not change)
    And the hardcoded maximum file writes is 5
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltHardcoded
    And the halt message contains "content unchanged"
    And the halt message contains "stale.go"
    And the write_count for "stale.go" is less than 5
    And exactly 2 tool_call events are logged for "edit_file" on "stale.go" (halt triggers on 3rd result before LLM call)

  Scenario: Content-hash only triggers on consecutive writes, not on first write
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                                   | text_response |
      | 1    | edit_file(path="new.go", old_str="x", new_str="y")           |               |
      | 2    |                                                              | "Done."       |
    And "new.go" has no prior file row in the Store (content_hash is nil)
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And no halt is triggered for content_hash mismatch (no previous hash to compare)
    And exactly 1 edit_file event is logged

  Scenario: ErrNoMatch on edit_file does not trigger false halt
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                                   | text_response |
      | 1    | edit_file(path="a.go", old_str="nonexistent_str", new_str="x") |               |
      | 2    |                                                              | "Let me read the file first." |
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And no HaltHardcoded halt is triggered
    And the model receives a tool result containing "ERROR: edit_file: zero matches"
    And the file content on disk is unchanged

  Scenario: Multiple halt conditions in the same turn produce exactly one halt event
    Given the Fake LLM is configured with a Responder that returns exactly 1 edit_file tool call per turn
    And the file "shared.go" already has write_count=4 in the Store
    And the next edit on "shared.go" has the same ContentHash as the previous write
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltHardcoded
    And exactly 1 halt event is logged for the session
    And the session status is "halted"
    And no LLM call occurs after the halt-triggering tool result

  # ---------------------------------------------------------------------------
  # Area 2b: Halt Detection — Delta/Semantic
  # ---------------------------------------------------------------------------

  Scenario: Delta check triggers halt on loop detection signal even when hardcoded thresholds are not hit
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                                   |
      | 1-5  | read_file(path="a.go") — same file, same tool                |
      | 6    | text: "Keep reading a.go to understand the code."            |
    And the delta check interval is 5 turns
    And the delta check LLM is a separate Fake that returns "YES, the agent is repeatedly reading the same file without making changes."
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltDelta
    And the halt message contains "delta halt"
    And the session has exactly 5 turns (the 5th turn triggers the delta check)
    And the session status is "halted"
    And the file write_count for "a.go" is 0 (no writes occurred — hardcoded check would not trigger)

  Scenario: Delta check passes without halting when no loop detected
    Given the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                                   | text_response |
      | 1    | read_file(path="a.go")                                       |               |
      | 2    | edit_file(path="a.go", old_str="x", new_str="y")            |               |
      | 3    | read_file(path="a.go")                                       |               |
      | 4    | create_file(path="b.go", content="pkg b")                   |               |
      | 5    | read_file(path="b.go")                                       |               |
      | 6    |                                                              | "Done."       |
    And the delta check interval is 5 turns
    And the delta check LLM returns "NO, the agent is making progress."
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And no delta halt event is logged
    And the session completes all 6 turns normally

  Scenario: Delta check does not halt if the LLM call itself fails
    Given the Fake LLM is configured with a Responder that returns 6 tool-calling turns
    And the delta check LLM returns an error (network failure)
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And no halt is triggered despite the delta check error (the loop continues)
    And all 6 turns complete normally

  # ---------------------------------------------------------------------------
  # Area 3: Token Budget / Max Turns
  # ---------------------------------------------------------------------------

  Scenario: Cumulative token stop triggers before max_turns when token threshold crossed
    Given AgentConfig.ContextMaxTokens is 500
    And the Fake LLM is configured with a Responder that returns 8 tool-calling turns
    And each model response consumes exactly 200 tokens
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltTokenLimit
    And the halt message contains "cumulative tokens"
    And the session has exactly 3 turns (tokens: 200+200 = 400 < 425 threshold; 600 > 425 triggers halt on turn 3)
    And the session status is "halted"
    And no LLM call was made that would exceed the 85% threshold

  Scenario: Max-turns backstop triggers when token threshold is never crossed
    Given AgentConfig.ContextMaxTokens is 1000000 (very large context — token check never binds)
    And the Fake LLM is configured with a Responder that returns 60 tool-calling turns
    And each model response consumes exactly 1 token
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltMaxTurns
    And the halt message contains "max turns"
    And the session has exactly 50 turns
    And the session status is "halted"

  Scenario: Exact boundary — token limit not reached, session completes normally
    Given AgentConfig.ContextMaxTokens is 1000
    And the Fake LLM is configured with a Responder that returns:
      | call | tool_calls                                           | text_response | tokens_per_call |
      | 1    | edit_file(path="a.go", old_str="x", new_str="y")   |               | 200             |
      | 2    |                                                      | "Done."       | 50              |
    When TurnLoop.Run() is called
    Then the HaltReason code is HaltCompleted
    And cumulative tokens (250) is below 85% of 1000 (850)
    And the session completes normally with no halt for token limit or max turns
