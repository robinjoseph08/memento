# Worker Invariants

- The worker is in-process, PostgreSQL-backed, lease-based, cancellable, and limited to one active job per Worker. Claims use ordered `FOR UPDATE SKIP LOCKED` transactions.
- Handlers must honor context cancellation and be safe for replay. Provider effects and database completion cannot guarantee exactly-once delivery.
- Use permanent failure only for safe terminal input or state errors, retry scheduling for bounded retryable diagnostics, and rescheduling for successful recurring work that must not increment attempts.
- Store only allowlisted, secret-safe diagnostics. Never retain credentials, tokens, raw dependency bodies, Comment or search text, recipient addresses, or private source metadata.
- Completion, retry, failure, reschedule, and heartbeat updates must verify the lease owner and an unexpired lease. Lease loss cancels active work.
- Recovery hold and the optional traffic gate fence both ordinary claims and external dispatch. Shutdown stops claims, cancels and drains work, releases reclaimable leases, then closes PostgreSQL.
- Integration tests poll durable state and prove heartbeat protection, expiry reclaim, retry coalescing, bounded concurrency, cancellation, and ownership loss.
