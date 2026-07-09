# Full Spec Audit: Violations Document

## Summary
Comprehensive audit of the Go coding-agent harness against the project specification (agent-harness_spec.md).

## Severity Definitions
- **Critical**: Violates safety/scoping or prevents basic operation.
- **High**: Significant deviation from documented behavior, likely causing unpredictable outcomes.
- **Medium**: Inconsistent behavior or documentation gap; manageable but degrades quality.
- **Low**: Minor deviation or missing documentation/clarification.

## Violations

### 1. Dogfooding Status (High)
- **Reference**: §16 Rollout Plan
- **Finding**: The spec dictates that the harness must be tested on itself as a dogfooding exercise ("the new harness is then tested on itself, in a separate branch"). There is no record in the logs or repository history indicating this has occurred.
- **Status**: Outstanding.

### 2. Git/Branching Reality (Medium)
- **Reference**: §15 Git Management
- **Finding**: §15 states "each phase runs on its own branch" with "per-phase-audit-gated merges". The actual implementation, as noted in the log files and confirmed during the audit, used a flat commit sequence on `main`. While the spec §15 footnote clarifies this as intentional for the *bootstrap* process, the text is ambiguous for a future reader, potentially causing confusion about whether the final product must enforce branch-per-phase for *managed* projects.
- **Status**: Clarification needed.

### 3. Bash Path Scoping (Medium)
- **Reference**: §6.1 Path Scoping
- **Finding**: The spec acknowledges the limitation that `bash_exec` cannot be fully path-scoped. However, there is no explicit warning or documentation of this risk provided to the agent itself in the system prompt. Relying solely on the documentation in the spec file may not be sufficient for the agent to behave safely.
- **Status**: Mitigation needed.

### 4. Spec Audit Implementation (Low)
- **Reference**: §14 Spec Audit
- **Finding**: The per-phase checks are implemented, but the final cross-phase audit process is not automated, as evidenced by this manual audit run. While the spec requires both, the transition between them is not clearly defined in the harness binary.
- **Status**: Improvement recommended.
