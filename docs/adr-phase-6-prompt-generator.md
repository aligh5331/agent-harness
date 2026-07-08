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
20: - Strictly read-only for project code.
21: - Out of scope: Spec auditing (§14), automated harness execution wiring.
22: 
23: ## Fix Cycle 1
24: - **Problem:** `prompt-generator` agent configuration erroneously granted unrestricted `create_file` access, violating the principle of least privilege.
25: - **Resolution:** Restricted `create_file` paths to `.aa/templates/` and `.aa/agents/` (for briefings).
26: - **Tool Grant Update:** 
27:   ```yaml
28:   create_file: 
29:     paths:
30:       - ".aa/templates/*.yaml"
31:       - ".aa/agents/*.md"
32:   ```
33: - **Verification:** Agent can write briefings and manifests, but is structurally incapable of modifying project code in `internal/`, `docs/`, or elsewhere.
34: 
35: ## Worked Example: Short Spec
36: Given: A simple project spec needing only Architect and Builder for Phase 1.
37: Output: `.aa/templates/manifest.yaml` set to `Phase 1: [architect, builder]`.
38: Briefings: `architect-briefing-phase-1.md` and `builder-briefing-phase-1.md` created, pointing to expected prior-phase paths.

