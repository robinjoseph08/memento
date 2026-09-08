# Frontend conventions

## Organization

- Keep route composition in `app/components/pages`, feature UI in focused
  component directories, shared shadcn components in `app/components/ui`,
  TanStack Query definitions in `app/hooks/queries`, and generated API types in
  `app/types/generated`.
- Use nested React Router layouts for public, Onboarding, viewer, and Curator
  shells. Keep bookmarkable tabs, filters, and selected media in URL state.
- Every user-visible route must render the shared `PageTitle`. Use the page name
  while data loads, then replace it with the fetched entity name when that is
  more useful. Keep the `Page | Memento` format rather than setting
  `document.title` directly.
- Register every public route with `publicPageMetadata` in
  `pkg/server/metadata.go` so its title, description, canonical URL, and social
  card are present in the initial HTML. Do not register authenticated routes or
  include private data in this metadata.
- Do not call `fetch` from pages or components. Use the shared HTTP adapter and
  feature-owned TanStack Query definitions. Mutations own their related cache
  invalidation.
- Keep server state in TanStack Query, bookmarkable navigation state in the
  URL, temporary interaction state locally, and React context for genuinely
  application-wide concerns. Do not add another global state store without a
  concrete need.
- Build UI from Tailwind and selectively added shadcn components. Keep shadcn
  source in `app/components/ui`; do not add a second component system.
  Add available components with `mise exec -- pnpm dlx shadcn@latest add <name>`
  using the root `components.json`, then adapt imports and styles to this app.
  Keep the existing individual Radix packages and local `cn` helper rather than
  retaining redundant dependencies from registry output.
- Use Tailwind utilities for layout, typography, responsive behavior, and
  interaction styles. Keep `app/styles.css` limited to imports and theme tokens;
  do not add handwritten component selectors or `@apply` aliases.
- Every data-entry interaction must use a real HTML `form` and shared form
  conventions regardless of whether it uses controlled state or React Hook
  Form. Preserve native keyboard submission, consistent pending states, inline
  field errors, and unsaved-change protection.
- Use TanStack Query for server state and worker-status polling. Consume
  generated API types rather than recreating Go request and response contracts
  by hand.

## Forms and responsive navigation

- Field errors should tell the person how to correct the input, using plain
  language such as "Enter a display name." Never show API field names,
  validator rules, or implementation types as user-facing validation messages.
  Keep API field keys stable and render the backend's user-facing messages
  rather than trying to rewrite technical errors in the browser.
- Show field errors beside their inputs. A form-level validation summary should
  direct attention to those fields, not repeat the first error. Keep failures
  unrelated to a field, such as access denial, in a form-level message.
- Do not discard edited form values when background queries fail or refetch.
  Keep the form mounted and distinguish initial loading from refresh failures.
- When header content does not fit, move secondary information and actions into
  an accessible menu instead of hiding them. Keep the signed-in name and role,
  theme controls, and Sign out in the account menu on desktop and mobile.
  Primary navigation needs its own responsive treatment.
- Clickable controls use `cursor: pointer`, including menu items and open menu
  triggers. Use a quiet background change for menu hover and keyboard focus,
  not bright outlines on pointer hover. Account menus should not block their
  own trigger.
- Use shared menu and dialog primitives for keyboard navigation, Escape,
  outside-click dismissal, and focus restoration. When a menu opens a dialog,
  closing the dialog must return focus to a visible trigger.

## Testing

- Keep tests close to the behavior they cover and query rendered UI through
  accessible roles and names.
- Test feature behavior rather than the internal structure of shadcn
  components.
