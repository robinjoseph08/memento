# Event and Publication Invariants

- Event drafts, Moments, Staged updates, and Audience review are Curator-private. Recipients read only current published projections and must not learn hidden Moment structure, counts, gaps, or inaccessible ordering.
- Keep at most one net-coalescing Staged update per published Event. Changes that cancel before Publication leave no staged residue.
- Publication is one optimistic, locking transaction that validates every Audience, creates immutable Publication and revision history, replaces current placements, entitlement unions, covers, search and activity projections, and appends audit and outbox records. No mixed revision may become observable.
- Preview is read-only, authorization-filtered, capability-restricted, and excluded from Recipient activity.
- Withdrawal removes effective access immediately while preserving identity and history. Restoration requires freshly reviewed Audiences and a later Publication for every stale placement.
- Lock order is part of this module's interface. Coordinate Publication, staging, placement, access-generation, Attendance, and Withdrawal locks consistently and cover any change with real concurrency tests.
- Keep rollback injection at named Publication and Withdrawal steps, and test stale versions, retries, reused-Media entitlement unions, and concurrent authorization changes.
