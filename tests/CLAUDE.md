# Test Standards

- Test at the highest useful deterministic seam: package tests for pure rules, `httptest` for handler contracts, isolated PostgreSQL for persistence and concurrency, Playwright for browser-visible behavior, and dedicated suites for real dependency contracts.
- Prove behavior through executable inputs and observable outputs. Source-code inspection may enforce repository structure, but it is not evidence that a product, privacy, recovery, or deployment behavior works.
- Apply the root deterministic synchronization, resource isolation, cleanup, and flake-diagnosis rules to every harness here. Infrastructure helpers should make those constraints the default rather than leaving them to each test.
- Live dependency contract fixtures may publish test-only data stores only on loopback. Pass fixture credentials through dedicated `MEMENTO_TEST_*` environment variables, and seed provider-owned child records only after the provider API has created their parent records.
- Use the `integration` build tag and `internal/testdb` for real PostgreSQL behavior. Keep controlled clocks, randomness, DNS, dialers, and dependency failures injectable where outcomes depend on them.
- When a security surface changes, update both the authorization capability model and its concrete production evidence registry.
- Live dependency fixture diagnostics report only fixed stages, status codes, counts, and safe classified adapter errors. Never print credentials, fixture source paths, upstream bodies, request URLs, raw service logs, email addresses, or provider identities. Suppress raw tool output at fixture setup and teardown boundaries, and prove sanitization and cleanup through executable failure-path tests.
