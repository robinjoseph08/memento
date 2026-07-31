# Frontend Standards

- Build screens from domain components and domain-specific TanStack Query hooks rather than route-level monoliths or ad hoc fetch state.
- TanStack Query owns fetched server state and mutation synchronization. Query keys include every server-selection input, and successful mutations update or invalidate every affected projection.
- Use named wire types generated from Go in `types/generated/`. Every `apiJSON<T>` response and serialized shared-client request payload must have generated-type provenance. Do not hand-write duplicate wire shapes or edit generated files.
- Make state ownership explicit: URL search parameters hold bookmarkable navigation and structured filters, Query holds authoritative server state, and component state holds transient forms, selection, dialogs, and drafts. Editable local drafts must detect and rebase against newer server versions.
- Scope Event-workspace async callbacks to both the Event identity and a monotonic selection generation so an earlier visit cannot mutate a later visit to the same Event.
- Treat Publication and Withdrawal as leave-blocking operations. Close and evict Preview state when they start, and require authoritative Event recovery after an uncertain result before access-changing controls resume.
- Centralize all production global `fetch` usage in `api.ts`, regardless of URL, and send application traffic through its shared same-origin client. Include the Session CSRF token on mutations. Clear protected Query data whenever identity, Session, Invitation, Recovery, or offline transitions require it.
- Preserve operation parity across desktop and mobile even when controls differ. Test the control shown at each breakpoint and keep keyboard, focus, touch target, reduced-motion, overflow, and privacy behavior accessible.
