## Git Conventions

### Commit Message Format

Each commit should be in the format of `[{Category}] {Change description}`

**Categories** (used for changelog generation):

- `[Frontend]`, `[Backend]`, `[Feature]`, `[Feat]` → Features section
- `[Fix]` → Bug Fixes section
- `[Docs]`, `[Doc]` → Documentation section
- `[Test]`, `[E2E]` → Testing section
- `[CI]`, `[CD]` → CI/CD section
- Any other category → Other section

**Examples:**

```
[Frontend] Add dark mode toggle to settings page
[Backend] Add batch delete endpoint for books
[Fix] Resolve race condition in job worker
[E2E] Add tests for user authentication flow
[CI] Add release automation with GitHub Actions
```

## Validation

Run `mise check` to validate changes before pushing them. It is the fast, worktree-safe local gate for linting, generated types, unit tests, and the frontend build.

Run `mise ci` when the complete CI-equivalent suite is needed. It adds race detection, isolated PostgreSQL integration tests, development Compose validation, Caddy validation, and the production topology test.

Use `mise start` to run the root Docker Compose dependencies, the Go API with Air hot reload, and the Vite frontend together. Compose runs in the foreground and stops its services when the task exits. Air regenerates Tygo types before rebuilding the API.

## Agent skills

### Issue tracker

Issues and PRDs are tracked in this repository’s GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the five default triage labels, plus `spec` for the `to-spec` workflow. See `docs/agents/triage-labels.md`.

### Domain docs

Use the single-context domain documentation layout. See `docs/agents/domain.md`.
