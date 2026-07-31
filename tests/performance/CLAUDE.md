# Performance Evidence Standards

- This directory is a target-scale integration evidence harness using production services, SQL, and isolated supported PostgreSQL, not a microbenchmark substitute.
- Generate the fixture deterministically and set-wise. Preserve the normative cardinalities, data shape, baseline coverage, competing-work scenarios, warm-up, qualifying sample counts, monotonic durations, and nearest-rank p95 calculation.
- Capture machine-readable `EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)` evidence for required queries and report the revision, environment, cache state, fixture, samples, and limitations.
- Development runs with reduced fixtures or samples must remain visibly non-qualifying. Never relax a target, comparator, sample count, or fixture to accommodate a regression.
- Diagnose regressions in production queries, indexes, migrations, or modules. Revise a normative target only through an explicit product decision with new rationale.
- Qualifying runs write temporary artifacts under `tmp/performance`; completed reviewed evidence belongs in both `docs/performance-report.json` and `docs/performance-report.md`. Re-run it after changes that can affect the measured paths or environment.
