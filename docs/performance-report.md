# Target-scale performance report

Generated: `2026-07-30T17:15:16Z`  
Qualifying: `true`  
Git revision: `35192e3f2cb2952db5b020232cdc0bdf61844800` (dirty: `false`)  
Cache state: `warm`  
PostgreSQL: `PostgreSQL 17.7 on aarch64-unknown-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit`

## Fixture

100,000 Media items, 50 Recipients, 21 Events, largest Event 5000 placements, 500 reused Media items, 140 Audience entries, 50 Publication Recipients, 50 overlapping Recipients, a 500-item proposal Moment with 50 Attendance rows, 1000 Comments, 5000 Favorites, 100500 search documents, and 50 delivery activity rows. Fixture checksum: `1fc7b036c0c6c19cd728c5a2e2e825c466666bbfe56ffa42e7a989ad334401d4`.

## Baselines

| Operation | p95 | Target | Result | Scenario | Concurrency | Immich latency |
| --- | ---: | ---: | --- | --- | ---: | ---: |
| Liveness response | 8.458µs | 50ms | PASS | steady | 1 | 0s |
| Readiness response with healthy dependencies | 1.236209ms | 500ms | PASS | steady | 1 | 0s |
| Session validation plus simple authorization | 1.903542ms | 50ms | PASS | steady | 1 | 0s |
| Recipient timeline or Event page, up to 100 items | 8.19325ms | 300ms | PASS | steady | 1 | 0s |
| Curator work queue or People list | 3.660833ms | 300ms | PASS | steady | 1 | 0s |
| Authorized search first page | 43.058666ms | 500ms | PASS | steady | 1 | 0s |
| Comment, Favorite, preference, or seen-state mutation | 4.080083ms | 300ms | PASS | steady | 1 | 0s |
| Atomic Publication with 5,000 placements and 50 Recipients | 1.53153975s | 3s | PASS | steady | 1 | 0s |
| Audience proposal recalculation for 50 Recipients and 500 Moment items | 451.703792ms | 1s | PASS | steady | 1 | 0s |
| Eligible job start after available_at | 3.389791ms | 1m0s | PASS | steady | 1 | 0s |
| Notification dispatch start after coalescing closes | 4.925ms | 2m0s | PASS | steady | 1 | 0s |
| Full 100,000-item reconciliation | 11.767442708s | 30m0s | PASS | steady | 1 | 0s |
| Media proxy first-byte application overhead | 18.137251ms | 150ms | PASS | steady | 1 | 5ms |
| Application buffer bytes per active Media stream | 32768 B | 1048576 B | PASS | steady | 32 | 0s |

## Competing work

| Operation | p95 | Competitor | Concurrency |
| --- | ---: | --- | ---: |
| Recipient timeline or Event page, up to 100 items | 9.499208ms | reconciliation | 2 |
| Recipient timeline or Event page, up to 100 items | 10.683833ms | publication | 2 |
| Authorized search first page | 53.447167ms | notification dispatch | 2 |

## PostgreSQL plans and buffers

- `authorization`: warm cache, role `Session authorization`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `media_authorization`: warm cache, role `Simple Media authorization`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `gallery`: warm cache, role `Recipient timeline`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `search`: warm cache, role `Authorized search`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.
- `curator`: warm cache, role `Curator People list`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.

## Environment

- OS/architecture: `darwin/arm64`
- CPU: `Apple M3 Pro` (11 logical CPUs)
- Memory: 38654705664 bytes
- Go: `go1.26.5`
- Database size: 573836435 bytes
- Database pool: 16 connections
