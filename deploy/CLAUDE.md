# Deployment Standards

- Production deploys the fixed Memento GHCR image by `sha256` digest. Do not use tags, floating executable dependencies, or a Compose `build:` override.
- Keep configuration portable across operator Compose project names, networks, host paths, and dependency locations. PostgreSQL and Immich remain independently operated dependencies, normally on a private external network.
- Support both production topologies without operator-authored Compose changes: bundled Caddy on public HTTP and HTTPS with persistent Caddy state and redirect handling, or loopback HTTP behind a trusted host reverse proxy with narrowly configured proxy trust and no public cleartext listener.
- Preserve the single-container contract, non-root and read-only hardening, dropped capabilities, bounded shutdown, and file-backed secrets under `/run/secrets`. Never place secrets in images, YAML, ordinary environment examples, logs, or health responses.
- Keep Caddy responsible for external serving, TLS, static assets, SPA fallback, security headers, and proxying to the loopback Go API. Authorization and protected Media caching remain application concerns.
- Validate production changes with Compose policy validation, Caddy validation, and the production topology suite. A documented configuration is not complete until both supported modes have executable validation.
