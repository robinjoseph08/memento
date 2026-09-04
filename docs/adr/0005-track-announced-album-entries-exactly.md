# Track announced Album Entries exactly

Memento will record one durable association for each Person and Album Entry that has appeared in Onboarding or an approved Update Notification. This normalized set makes notification previews exact and idempotent across later access changes, including granting old media and revoking then restoring access. It costs more rows than an Album timestamp or revision cursor, but avoids access-history reconstruction and never duplicates an association for later notifications.
