# Mobile App conventions

The Mobile App is an Expo project for viewers only. It is its own package with
its own lockfile and shares no code with the web app in `app`. Read the parent
specification in issue #67 and ADR 0013 before adding to it.

## Organization

- Routes live in `src/app` and stay thin. Screens live in `src/screens`, shared
  UI in `src/components`, TanStack Query definitions in `src/hooks/queries`,
  and everything without React in `src/lib`.
- One HTTP adapter, `src/lib/http.ts`, owns every request and holds the
  Installation origin. Nothing else calls `fetch`, and ESLint enforces it.
  Screens and components reach the server through query definitions.
- Everything the app stores is keyed by Installation origin, and so is every
  query key. `src/lib/storage.ts` owns the key format. The app connects to one
  Installation at a time, but nothing may assume there will only ever be one.
- What the server says is server state and stays in TanStack Query. Do not
  copy it into storage or React context. Keep query functions free of side
  effects.
- Payload types come from `src/types/generated`, which `mise tygo` writes from
  `mobile/tygo.yaml`. Do not restate Go response shapes by hand. Only the
  viewer endpoints in ADR 0013 are the app's to call.
- Colors and fonts in `src/theme.ts` mirror the web's tokens in
  `app/styles.css`. Change them together. Navigation and controls follow each
  platform instead of imitating the web.
- Plain `http` is for development. Gate it on `__DEV__` so a store build can
  never accept it.
- The browser build that Expo serves is a development preview for looking at
  a screen without a phone. Memento does not ship it. Keep it rendering, but
  never trade native behavior for it.
- Install Expo packages with `pnpm expo install` so versions match the SDK.
  Expo Go is the dev client until Push needs a development build, so do not
  add a library that needs custom native code without saying so first.

## Testing

- Tests use Jest and React Native Testing Library. The HTTP adapter is the one
  seam: pass `fakeHTTP` from `src/testing/fake-http.ts` to `AppProviders`.
  Storage and safe-area insets use their libraries' own in-memory versions
  from `jest.setup.js`. Do not mock the app's own modules.
- Screen tests assert on visible text and accessible roles. Await everything
  the screen goes on to load, so no update lands after the test ends.
- The real adapter has its own tests against a stubbed network in
  `src/lib/http.test.ts`.
- Run `mise lint:mobile` and `mise test:mobile`. There is no device automation.
  Anything that needs a phone goes in the PR as a manual check.
