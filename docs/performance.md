# Target-scale performance proof

Memento includes a manual performance suite for the baseline targets in the product and architecture specification. The suite uses production services and SQL against PostgreSQL 17.7. It is intentionally separate from the normal CI suite because the target-scale fixture and repeated measurements are sensitive to shared-runner load.

## Run it

Prerequisites are Docker and the pinned tools installed by `mise`.

```sh
mise run test:performance
```

The runner creates an isolated PostgreSQL container on a dynamic loopback port, applies every migration, generates the fixture, runs the measurements, and removes the container. It writes:

- `tmp/performance/report.json`, including samples and complete PostgreSQL JSON plans;
- `tmp/performance/report.md`, a human-readable summary.

Set `MEMENTO_TEST_DATABASE_URL` to use an existing disposable PostgreSQL database. The test still creates and removes an isolated schema. Never point it at a production database.

The tagged harness is compiled without running the fixture during the normal test gate:

```sh
mise run test:performance-compile
```

## Reproducible fixture

The deterministic fixture contains:

- 100,000 Media items and active Immich backings;
- 50 non-Curator Recipients with current Sessions and notification preferences;
- 21 Events, including 5,000-placement Events and a 500-item Audience-proposal Moment;
- 50 Recipients represented across overlapping two-Recipient Moment Audiences in the Publication target Event;
- confirmed Attendance for all 50 Recipients on the 500-item Audience-proposal Moment;
- Media reused across Events;
- Comments, Favorites, normalized search documents, notification batches, and engagement activity.

Set-based PostgreSQL inserts keep fixture generation bounded. The suite runs `ANALYZE`, validates cardinalities and required overlap, and records a checksum over the resulting shape before measurement.

## Measurement rules

- Durations use Go's monotonic clock and nearest-rank p95.
- Cheap operations use 100 measured samples by default.
- Stateful operations use 20 measured samples by default.
- Full reconciliation is measured once as a completion target.
- The process and PostgreSQL cache are warmed before recorded samples.
- Every result records cache state, scenario, concurrency, and injected Immich latency.
- Reconciliation, Publication, and notification-dispatch competing-work scenarios are reported separately.
- Notification start samples persist an exact 15-minute batch window, schedule the production immediate-email job kind at `closes_at`, and record production worker handler entry.
- Proxy overhead subtracts observed controlled upstream blocking time.
- Stream evidence holds 32 concurrent 16 MiB streams open, measures Go heap growth per stream, and records the largest application-requested upstream read. The larger observation is evaluated against 1 MiB. The downstream test writer discards bytes so receiver buffering is not attributed to the application.
- Publication uses 5,000 placements across 50 Recipients with overlapping Moment Audiences and checks the production transaction boundary. Existing failure-boundary tests continue to prove rollback at every durable step.

The suite fails if any baseline is absent, required fixture evidence is missing, plans are invalid, or a measured target is missed.

Sample counts can be lowered only for harness development:

```sh
MEMENTO_PERFORMANCE_CHEAP_SAMPLES=2 \
MEMENTO_PERFORMANCE_OPERATION_SAMPLES=2 \
MEMENTO_PERFORMANCE_PUBLICATION_SAMPLES=2 \
MEMENTO_PERFORMANCE_STREAM_CONCURRENCY=2 \
mise run test:performance
```

A reduced run is marked `qualifying: false`; report validation enforces the default minimum sample counts for qualifying evidence.

## Interpreting results

The committed [qualifying report](performance-report.md) records the tested host, database version and size, fixture checksum, warm-cache state, injected Immich latency, concurrency, steady-state results, and competing-work results. Its matching [JSON artifact](performance-report.json) includes every sample and `EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)` output for representative authorization, gallery, search, and Curator queries.

Results are evidence for the recorded environment, not a claim about Immich storage throughput or arbitrary operators' hardware. Re-run the suite after query, index, PostgreSQL, Go, or target-scale fixture changes.
