# Use server-side sessions

Memento will issue opaque random session tokens, retain only their hashes in PostgreSQL, and expire them after 180 days without use. Server-side state makes Person deactivation, identity unlinking, and sign-out across devices effective immediately, which matters more than avoiding a small session table. Revoked sessions are deleted, expired sessions are rejected and removed opportunistically, and no session-history model is retained for the MVP.
