# Agent Instructions

## Guidelines

- Read the project README and task-relevant docs before making changes.
  Check for additional AGENTS.md instructions in the affected directories.
- Follow existing code style, architecture, and conventions.
- Keep changes focused on the task; avoid unrelated refactoring or formatting.
- Prefer early returns over deeply nested conditionals.
- Handle error states explicitly. In Go, propagate or handle errors with
  useful context; do not silently discard them.
- In Vue, follow existing Composition API and TypeScript patterns.
  Avoid `any` unless necessary and justified.
- Use semantic HTML. Prefer native accessible controls and add ARIA
  attributes when needed.
- Do not introduce new dependencies without a clear justification.

## Verification

- Inspect the root Makefile and web/package.json for supported commands.
  Prefer existing project workflows.
- Run the smallest verification scope that meaningfully validates the change:
  - Go: relevant package tests first.
  - Frontend: relevant tests, if available.
- Expand verification for cross-module changes, shared behavior changes,
  architectural changes, or failures. Run `go test ./...` when warranted.
- For frontend code or build configuration changes, run
  `cd web && npm run build`; it runs vue-tsc and builds the embedded UI.
- When Go verification depends on updated embedded UI assets, build the
  frontend first, then run the relevant Go build or Makefile target.
- The repo has no separate linter. Do not add one or create missing test
  scripts merely to satisfy these instructions.
- Documentation-only changes generally do not require builds or tests.
- Before finishing, report the checks run and any checks that could not
  be completed, with reasons.

## Git

- Preserve existing user changes. Do not revert or overwrite unrelated work.
- Do not commit unless requested.
- When committing, use conventional commit messages matching history,
  e.g. `feat(web): ...` or `fix(tunnel): ...`.
- Stage only files related to the current task.
- Do not push unless explicitly requested.
