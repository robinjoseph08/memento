# Use River for durable work

Memento will use River through its `database/sql` adapter in polling mode, sharing Bun's PostgreSQL pool and running inside the API process. River supplies durable claiming, crash recovery, retries, uniqueness, and graceful shutdown without another service or connection pool. Memento will expose task-specific interfaces, keep user-visible status in its own tables, use bounded SMTP and ffprobe queues, and avoid River scheduling or UI until a concrete need appears.
