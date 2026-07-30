# Target-scale performance report

Generated: `2026-07-30T16:54:10Z`  
Qualifying: `true`  
Git revision: `22d0057b5ab5b278e6fd866a03e9c45b14e08d7c` (dirty: `false`)  
Cache state: `warm`  
PostgreSQL: `PostgreSQL 17.7 on aarch64-unknown-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit`

## Fixture

100,000 Media items, 50 Recipients, 21 Events, largest Event 5000 placements, 500 reused Media items, 140 Audience entries, 50 Publication Recipients, 50 overlapping Recipients, 1000 Comments, 5000 Favorites, 100500 search documents, and 50 delivery activity rows. Fixture checksum: `9c936ec6e553f2ea0f3e17e116c9d77b3c9d7fa7b43c82b483cac07e8161e29d`.

## Baselines

| Operation | p95 | Target | Result | Scenario | Concurrency | Immich latency |
| --- | ---: | ---: | --- | --- | ---: | ---: |
| Liveness response | 11.5µs | 50ms | PASS | steady | 1 | 0s |
| Readiness response with healthy dependencies | 950.166µs | 500ms | PASS | steady | 1 | 0s |
| Session validation plus simple authorization | 1.718084ms | 50ms | PASS | steady | 1 | 0s |
| Recipient timeline or Event page, up to 100 items | 8.248584ms | 300ms | PASS | steady | 1 | 0s |
| Curator work queue or People list | 5.848459ms | 300ms | PASS | steady | 1 | 0s |
| Authorized search first page | 43.343083ms | 500ms | PASS | steady | 1 | 0s |
| Comment, Favorite, preference, or seen-state mutation | 6.106375ms | 300ms | PASS | steady | 1 | 0s |
| Atomic Publication with 5,000 placements and 50 Recipients | 1.77161675s | 3s | PASS | steady | 1 | 0s |
| Audience proposal recalculation for 50 Recipients and 500 Moment items | 538.171ms | 1s | PASS | steady | 1 | 0s |
| Eligible job start after available_at | 4.245292ms | 1m0s | PASS | steady | 1 | 0s |
| Notification dispatch start after coalescing closes | 2.144458ms | 2m0s | PASS | steady | 1 | 0s |
| Full 100,000-item reconciliation | 11.76170425s | 30m0s | PASS | steady | 1 | 0s |
| Media proxy first-byte application overhead | 28.650417ms | 150ms | PASS | steady | 1 | 5ms |
| Application buffer bytes per active Media stream | 32768 B | 1048576 B | PASS | steady | 32 | 0s |

## Competing work

| Operation | p95 | Competitor | Concurrency |
| --- | ---: | --- | ---: |
| Recipient timeline or Event page, up to 100 items | 11.293542ms | reconciliation | 2 |
| Recipient timeline or Event page, up to 100 items | 10.499959ms | publication | 2 |
| Authorized search first page | 54.069958ms | notification dispatch | 2 |

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
- Database size: 548138131 bytes
- Database pool: 16 connections
