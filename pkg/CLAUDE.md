# Backend Standards

- Keep HTTP handlers thin: authenticate under the route policy, bind and validate a named contract, call the domain service, translate safe errors, and write the response.
- Services own domain rules, transactions, authorization-affecting invariants, concurrency control, audit, projections, and durable handoffs. Cross-domain workflows use explicit service or transaction seams, never handler-to-handler calls.
- Define every Memento HTTP JSON request and response as a named exported Go struct with `snake_case` fields. The contract gate follows package-local bind wrappers and enforces roots at Echo `Bind`, `JSON`, and `JSONPretty` seams. Add frontend-visible contracts to `tygo.yaml`; do not expose persistence records as accidental wire contracts. Dependency adapters keep their provider-specific DTOs private.
- Use one canonical domain type for each identity and value within service code. Convert transport input at the handler seam instead of carrying duplicate representations through the domain.
- Declare a primary authorization policy for every route and repeat resource authorization in service queries. Apply authorization before matching, grouping, projection, totals, or existence-revealing lookup, and fail closed with non-enumerating errors where disclosure matters.
- Put pure rules and mappings in package tests. Use isolated PostgreSQL integration tests for transactions, constraints, projections, lock ordering, authorization queries, and concurrency.
