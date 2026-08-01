# Browser Evidence Standards

- Use Playwright for browser-observable workflow, authorization, accessibility, responsive parity, and privacy evidence. Focused mocked UI states do not replace full-application evidence when persistence or server authorization matters.
- Run applicable behavior on desktop and mobile Chromium and Firefox projects. Operation parity does not require identical controls, but every supported workflow must remain possible and private at each breakpoint.
- Prefer role and label locators, observable assertions, and bounded `expect.poll`. Do not use fixed waits or brittle structure selectors when an accessible interface exists.
- Assert request method, body, CSRF behavior, visible result, focus, accessible state, and the absence of forbidden network requests. Hidden content must be absent from the DOM, accessibility tree, counts, keyboard order, and requests, not merely covered by CSS.
- Keep ordinary tests isolated from service workers. PWA tests alone enable them and must prove shell revisioning, offline behavior, and exclusion of protected or private responses.
- Full-application evidence uses the browser preview harness, real production handlers, disposable PostgreSQL, and a distinct seeded Session and record for every Playwright project. Do not intercept its API requests.
- CI retries are diagnostic containment, not a flake fix. Reproduce and remove nondeterministic synchronization or resource sharing before merging.
