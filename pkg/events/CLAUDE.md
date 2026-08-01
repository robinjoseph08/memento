# Event and Publication Invariants

- Event drafts, Moments, Staged updates, and Audience review are Curator-private. Recipients read only current published projections and must not learn hidden Moment structure, counts, gaps, or inaccessible ordering.
- Represent Source metadata suggestions per changed field. An unchanged field is null, while a changed field may deliberately suggest an empty value; suggestions never mutate Event metadata without Curator action.
- Use a client-generated Event idempotency identity so retrying draft creation after an uncertain response returns the committed Event instead of creating a duplicate.
- Keep at most one net-coalescing Staged update per published Event. Changes that cancel before Publication leave no staged residue.
- Publication is one optimistic, locking transaction that validates every Audience, creates immutable Publication and revision history, replaces current placements, entitlement unions, covers, search and activity projections, and appends audit and outbox records. No mixed revision may become observable.
- Preview is read-only, authorization-filtered, capability-restricted, and excluded from Recipient activity.
- Withdrawal removes effective access immediately while preserving identity and history. Restoration requires freshly reviewed Audiences and a later Publication for every stale placement.
- Lock order is part of this module's interface. Coordinate Publication, staging, placement, access-generation, Attendance, and Withdrawal locks consistently and cover any change with real concurrency tests.
- Keep rollback injection at named Publication and Withdrawal steps, and test stale versions, retries, reused-Media entitlement unions, and concurrent authorization changes.
