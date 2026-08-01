# Source and Reconciliation Invariants

- Source discovery and reconciliation create private Curator work, never Recipient authority. Browser contracts expose only Memento identities and allowlisted normalized fields, not Immich IDs, URLs, paths, ownership, library data, or raw DTOs.
- Preserve durable Source identity and disposition. Album absence marks a Source missing rather than deleting it, and ignore or restore uses optimistic version checks.
- One complete, stable membership pass may stage additions. Removals require two consecutive stable, identical passes; failed, partial, or unstable scans do not advance evidence, and a different stable set starts a new sequence.
- Perform dependency reads outside long database lock scopes, then apply results only if the exact database snapshot remains current. Keep pagination bounded, deduplicated, cancellable, and safe under retry. Never infer an asset's availability from album membership evidence or an incomplete probe.
- Reconciliation jobs coalesce by durable idempotency key. Additions and removals remain private staged work until Publication, and add-then-remove cancellation leaves no residue.
- Route future Media by its usable local capture day only when retained `source_days` map that day to exactly one merged Moment. Keep split-day, unknown-date, and unusable-date Media unassigned, and preserve review state when a routed addition cancels.
- Treat album membership evidence and per-asset delivery availability as distinct facts; neither may silently transfer identity or authorization.
