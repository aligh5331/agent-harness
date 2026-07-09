# ADR 007: Automated Spec Auditing

## Status
Proposed

## Context
Manual phase auditing using `forensic-per-phase-audit-template.md` is error-prone and doesn't scale.

## Decision
1. Formalize `--audit-phase N` for per-phase checks.
2. Formalize `--audit-full` for cross-phase consistency checks.
3. Both outputs MUST be structured markdown for automation.

## Consequences
- Automated audit runs.
- Standardized forensic reporting.
