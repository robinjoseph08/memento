# Target-scale performance report

- Generated: `2026-08-01T07:24:36Z`
- Qualifying: `true`
- Git revision: `8f8841d534d2d718b25cc36a3e0dd71f60cd6167` (dirty: `false`)
- Cache state: `warm`
- PostgreSQL: `PostgreSQL 17.7 on aarch64-unknown-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit`

## Fixture

100,000 Media items, 50 Recipients, 21 Events, largest Event 5000 placements, 500 reused Media items, 208 Audience entries, 50 Publication Recipients, 50 overlapping Recipients, a 500-item proposal Moment with 50 Attendance rows, 1000 Comments, 5000 Favorites, 100500 search documents, and 50 delivery activity rows. Fixture checksum: `aa8c7b474da5ee2eb58005205b32f578fe984488b5471268d1b2f9bb5b1d2656`.

## Baselines

| Operation | p95 | Target | Result | Scenario | Concurrency | Immich latency |
| --- | ---: | ---: | --- | --- | ---: | ---: |
| Liveness response | 3.542µs | 50ms | PASS | steady | 1 | 0s |
| Readiness response with healthy dependencies | 1.065167ms | 500ms | PASS | steady | 1 | 0s |
| Session validation plus simple authorization | 1.641083ms | 50ms | PASS | steady | 1 | 0s |
| Recipient timeline or Event page, up to 100 items | 226.042709ms | 300ms | PASS | steady | 1 | 0s |
| Curator work queue or People list | 3.321125ms | 300ms | PASS | steady | 1 | 0s |
| Authorized search first page | 43.371625ms | 500ms | PASS | steady | 1 | 0s |
| Comment, Favorite, preference, or seen-state mutation | 3.802542ms | 300ms | PASS | steady | 1 | 0s |
| Atomic Publication with 5,000 placements and 50 Recipients | 2.041745125s | 3s | PASS | steady | 1 | 0s |
| Audience proposal recalculation for 50 Recipients and 500 Moment items | 689.990208ms | 1s | PASS | steady | 1 | 0s |
| Eligible job start after available_at | 2.236666ms | 1m0s | PASS | steady | 1 | 0s |
| Notification dispatch start after coalescing closes | 4.806ms | 2m0s | PASS | steady | 1 | 0s |
| Full 100,000-item reconciliation | 10.914141959s | 30m0s | PASS | steady | 1 | 0s |
| Media proxy first-byte application overhead | 18.933583ms | 150ms | PASS | steady | 1 | 5ms |
| Application buffer bytes per active Media stream | 32768 B | 1048576 B | PASS | steady | 32 | 0s |

## Competing work

| Operation | p95 | Competitor | Concurrency |
| --- | ---: | --- | ---: |
| Recipient timeline or Event page, up to 100 items | 5.292667ms | reconciliation | 2 |
| Recipient timeline or Event page, up to 100 items | 5.169625ms | publication | 2 |
| Authorized search first page | 49.567083ms | notification dispatch | 2 |

## PostgreSQL plans and buffers

- `authorization`: warm cache, role `Session authorization`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `media_authorization`: warm cache, role `Simple Media authorization`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `gallery`: warm cache, role `Recipient direct date jump`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `chronology`: warm cache, role `Complete authorized Recipient chronology`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `search`: warm cache, role `Authorized search`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `curator`: warm cache, role `Curator People list`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.

## Limitations

- Results characterize this recorded host and warm PostgreSQL cache, not arbitrary operator hardware or Immich storage throughput.
- Complete Recipient chronology correctness and its query plan are exercised at target scale, but the product specification defines no standalone chronology latency target.
- Controlled local dependencies do not model networked PostgreSQL or Immich latency except where a metric records injected dependency delay.

## Environment

- OS/architecture: `darwin/arm64`
- CPU: `Apple M3 Pro` (11 logical CPUs)
- Memory: 38654705664 bytes
- Go: `go1.26.5`
- Database size: 640339091 bytes
- Database pool: 16 connections
