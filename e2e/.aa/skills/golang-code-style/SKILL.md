---
name: golang-code-style
description: Go code style conventions — line length, control flow, variable declarations.
---

# Go Code Style

## Line Length
No rigid limit, but beyond ~120 chars MUST be broken at semantic boundaries.

## Variable Declarations
Use `:=` for non-zero values, `var` for zero-value initialization.

## Control Flow
Errors and edge cases MUST be handled first (early return).
No unnecessary `else` — use default-then-override.
