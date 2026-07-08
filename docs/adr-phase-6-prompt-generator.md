## Decision
This phase automates the harness's meta-programming by introducing a Phase 0 Generator. This agent reads a project spec, determines the necessary phases and agent stages, and writes out the required briefing files and loop templates.

## Context
The harness currently relies on manually bootstrapped briefings (Phase 1-5). To support new projects, we must automate the creation of `docs/adr-phase-N-*.md` and briefing templates.

## Options considered
- **In-process generation:** Use the meta-agent to generate files directly.
- **CLI tool generation:** Separate generator tool.
- **Chosen:** Agent-based generation using existing harness infrastructure (Read/Create tools).

## Chosen approach
1. **Input:** `--spec path/to/project_spec.md`.
2. **Manifest:** YAML file at `.aa/templates/manifest.yaml` mapping phase number to agent list.
3. **Briefings:** Agent generates `architect-briefing-phase-N.md` (etc.) per phase, following existing naming/content patterns.
4. **Templates:** A set of genuinely generic `<agent>-template.md` files living as embedded defaults in the harness.
5. **Validation:** Harness loads the generated manifest to determine phase agent-sets.

## Constraints and risks
- Strictly read-only for project code.
- Out of scope: Spec auditing (§14), automated harness execution wiring.

## Worked Example: Short Spec
Given: A simple project spec needing only Architect and Builder for Phase 1.
Output: `.aa/templates/manifest.yaml` set to `Phase 1: [architect, builder]`.
Briefings: `architect-briefing-phase-1.md` and `builder-briefing-phase-1.md` created, pointing to expected prior-phase paths.
