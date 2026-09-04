# Frontend conventions

## Organization

- Keep route composition in `app/components/pages`, feature UI in focused
  component directories, shared shadcn components in `app/components/ui`,
  TanStack Query definitions in `app/hooks/queries`, and generated API types in
  `app/types/generated`.
- Use nested React Router layouts for public, Onboarding, viewer, and Curator
  shells. Keep bookmarkable tabs, filters, and selected media in URL state.
- Do not call `fetch` from pages or components. Use the shared HTTP adapter and
  feature-owned TanStack Query definitions. Mutations own their related cache
  invalidation.
- Keep server state in TanStack Query, bookmarkable navigation state in the
  URL, temporary interaction state locally, and React context for genuinely
  application-wide concerns. Do not add another global state store without a
  concrete need.
- Build UI from Tailwind and selectively added shadcn components. Keep shadcn
  source in `app/components/ui`; do not add a second component system.
- Every data-entry interaction must use a real HTML `form` and shared form
  conventions regardless of whether it uses controlled state or React Hook
  Form. Preserve native keyboard submission, consistent pending states, inline
  field errors, and unsaved-change protection.
- Use TanStack Query for server state and worker-status polling. Consume
  generated API types rather than recreating Go request and response contracts
  by hand.

## Testing

- Keep tests close to the behavior they cover and query rendered UI through
  accessible roles and names.
- Test feature behavior rather than the internal structure of shadcn
  components.
