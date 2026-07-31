# Immich Adapter Contract

- This package is Memento's server-side adapter for the pinned Immich version and least-privilege operation set. Browsers never call Immich or receive its credentials, direct URLs, or raw DTOs. Recipient contracts expose no Immich IDs, paths, or face coordinates; Curator repair contracts may expose only the explicitly normalized evidence required for manual repair.
- Normalize dependency responses into narrow Memento types. Provider request and response roots must use named DTOs, and new provider DTO fields must not use `json.RawMessage`. The contract gate permits local maps used for duplicate and case validation but rejects maps and anonymous structs at the JSON client seams.
- Return only safe classified errors. Never include keys, headers, upstream bodies, URLs, paths, or unnormalized provider data in errors, logs, health, or worker diagnostics.
- For Media, validate Range and cache validators, allowlist statuses and response headers, bound derivatives, and stream videos and originals. Callers own and must close returned bodies.
- Follow only a bounded number of same-origin redirects. Reject cross-origin, credential-bearing, non-HTTP, or otherwise unsafe targets before forwarding credentials.
- Keep hermetic malformed and adversarial tests, fuzz parsing surfaces, and the live pinned contract suite. Supporting a later Immich release requires changing the pin and passing the complete contract rather than widening acceptance speculatively.
