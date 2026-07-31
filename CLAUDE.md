## Guidance Hierarchy

`CLAUDE.md` is the canonical root guidance, and `AGENTS.md` remains a symlink to it for agent compatibility. Before changing a file, read this file and every nearer `CLAUDE.md` in its directory ancestry.

Place durable engineering rules in the narrowest directory that owns them. Child guidance extends its ancestors and should reference rather than repeat broader rules. When a change establishes or alters a reusable pattern, invariant, test seam, deployment contract, or evidence obligation, update the nearest relevant guidance in the same change. Move guidance when its scope narrows instead of leaving conflicting copies.

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

During implementation and review-fix rounds, run the targeted tests for the changed behavior followed by `mise check:quiet`. It runs the fast, worktree-safe `mise check` gate for linting, generated types, unit tests, and the frontend build. Successful task output is suppressed; a failure prints the failed-task summary and the original captured output. Do not rerun the gate with grep or other filters to rediscover the failure.

After implementation and review have converged, run `mise ci:quiet` once as the final local gate immediately before pushing. It runs `mise ci`, which adds race detection, browser tests, isolated PostgreSQL integration tests, the Immich contract, development Compose validation, Caddy validation, and the production topology test. If this final gate finds a defect, return to targeted tests and `mise check:quiet` while fixing it, then repeat the final gate only after the changes stabilize.

Use `mise start` to run the root Docker Compose dependencies, the Go API with Air hot reload, and the Vite frontend together. Compose runs in the foreground and stops its services when the task exits. Air regenerates Tygo types before rebuilding the API.

### Test reliability

- Tests must be deterministic under full-suite load. Do not use fixed sleeps to wait for asynchronous outcomes, and do not rely on test order, shared ports, shared database state, or external network availability.
- For asynchronous behavior, poll the observable condition with a bounded deadline. A timeout should report the last observed state so CI failures are diagnosable.
- Give every parallel test an isolated database, schema, temporary directory, Compose project, and port allocation as applicable. Cleanup must tolerate partial setup and always run.
- Reproduce intermittent failures by looping the smallest affected test under contention before changing it. Fix the synchronization or isolation boundary instead of adding retries or widening arbitrary sleeps.
- A rerun that passes confirms a flake; it does not fix one. Inspect the failed run, add a deterministic regression signal where possible, and remove the source of nondeterminism before merging.

## Agent skills

### Issue tracker

Issues and PRDs are tracked in this repository’s GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the five default triage labels, plus `spec` for the `to-spec` workflow. See `docs/agents/triage-labels.md`.

### Domain docs

Use the single-context domain documentation layout. See `docs/agents/domain.md`.
