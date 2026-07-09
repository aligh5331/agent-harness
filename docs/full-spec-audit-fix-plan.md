# Full Spec Audit: Fix Plan

## Summary
Proposed remediation for the violations identified in `docs/full-spec-audit-violations.md`.

## Proposed Fixes

### 1. Dogfooding Exercise
- **Action**: Create a new branch `dogfood-test` and point the harness at its own repository, running a single full pipeline execution to verify self-referential capability.
- **Priority**: High

### 2. Spec Clarification
- **Action**: Append an addendum to §15 in `agent-harness_spec.md` clarifying that the flat commit history on `main` was a bootstrap-only exception and that future managed projects will enforce the branch-per-phase model.
- **Priority**: Medium

### 3. Bash Path Scoping Warning
- **Action**: Explicitly include a "Security Warning: Bash Execution" section in the system prompt for agents granted `bash_exec`. This warns them of the path-scoping limitation.
- **Priority**: Medium

### 4. Spec Audit Automation
- **Action**: Add a `make audit` command that automates the check of the final repository state against the spec rules, instead of relying on a manual audit process.
- **Priority**: Low
