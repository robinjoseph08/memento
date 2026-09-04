# Use a modular monolith

Memento will remain one Go binary backed by one PostgreSQL database, with the React application and in-process worker included in the same deployment. Application behavior will live in a few feature modules under `pkg`, while Google, Immich, SMTP, and work execution vary through narrow adapters. This keeps deployment to one Docker image and allows transactions to protect local state without introducing a separate worker process, message broker, Redis, or distributed coordination.
