# Repair Invariants

- Immich Person, face, and Media changes create private Curator repair evidence. Automatic reconciliation may update private inventory, links, candidates, and suggestion eligibility, but it never changes Attendance, Audience state, Recipient access, entitlements, or published authorization.
- Confirmation applies only the exact evidence the Curator reviewed. Revalidate dependency identity and availability outside long database lock scopes, then reject changed, missing, ambiguous, or already claimed evidence without partial mutation.
- Preserve stable Memento Person and Media identities across repairs. Provider names, paths, checksums, IDs, face coordinates, and conflict evidence remain private and appear only in allowlisted Curator contracts.
