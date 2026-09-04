# Backend conventions

## Organization

- Keep important application code under `pkg`. Use feature packages such as
  `identity`, `publishing`, `media`, `notifications`, and `worker`.
- Keep Bun table models and small shared value types in `pkg/models`. Models may
  define persistence and serialization shape, but must not contain business
  behavior or orchestration.
- Use application-generated UUIDv7 values for Memento entity IDs and store them
  in PostgreSQL's native `uuid` type. Keep Immich IDs as opaque external
  references.
- Store system events as UTC `timestamptz` values. Store Immich local capture
  time as `timestamp without time zone`; Moment grouping and media ordering must
  not reinterpret it using the server timezone.
- Enforce ownership, uniqueness, and same-Album relationships with PostgreSQL
  foreign keys, checks, and unique constraints. Do not rely on Go validation
  alone for data invariants.
- Use last-write-wins for ordinary Curator edits. Do not add generic record
  versions or conflict UI without evidence of concurrent editing. Protect
  one-time state transitions with targeted conditional updates and constraints.
- Request types belong to the feature package and must not reuse writable
  database models. Response types may reuse a model only when its complete
  shape is intentionally public. Use feature-owned projection types for
  viewer-dependent or aggregate responses.
- Add request and response structs with their validation tags to `types.go`.
  Create `validation.go` only when custom validation logic exists.
- Use `routes.go` for route registration and `handlers.go` for HTTP translation.
  Handlers must not coordinate multi-step persistence work.
- Expose complete use cases from the feature's module. The module owns the
  transaction and hides query ordering, derived state, and invariants. Do not
  create CRUD-shaped abstractions for each model.
- Split large feature packages by behavior, such as `import.go`, `sync.go`, or
  `access.go`, rather than splitting every function into its own file.
- Keep cross-package interfaces narrow and add them only where behavior varies.
  External systems must sit behind adapters that can be replaced in tests.
- Commit database state and durable work together. Perform Immich, SMTP, and
  `ffprobe` calls outside database transactions.
- Keep media bytes in Immich. Serve authorized media through private,
  content-versioned browser URLs and standard HTTP caching rather than adding a
  persistent Memento media cache or service worker.
- Generate frontend types with Tygo only from types intentionally included in
  its configuration.
- Return structured field errors for validation failures so every frontend form
  can preserve values, show inline errors, and focus the first invalid field.

## Testing

- Run independent tests with `t.Parallel()` by default.
- Support PostgreSQL 14 and newer. Run normal integration tests on the current
  development version and keep a focused PostgreSQL 14 migration and module
  check for shared-container Immich deployments.
- Test module interfaces against real PostgreSQL. Give each top-level database
  integration test an isolated schema, and group related table-driven scenarios
  so migrations run once for the group rather than once per case.
- Keep test connection pools small so parallel packages do not exhaust
  PostgreSQL connections.
- Test pure behavior without a database and HTTP handlers with small fake
  modules. Test external adapters against recorded or local test counterparts.
- Control worker execution in tests. Do not use sleeps to wait for work.
- Test the Immich adapter quickly with local HTTP fixtures. Keep a separate
  black-box compatibility suite against exact official Immich release images,
  create fixtures through supported APIs, and never read or seed Immich tables.
- Measure database setup cost as migrations grow. If it becomes material,
  optimize the shared test helper rather than reducing isolation or broadly
  disabling parallelism.
