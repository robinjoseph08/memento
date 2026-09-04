# Browser-test conventions

- Support current Chrome, Firefox, desktop Safari, and iOS Safari. Run the
  focused Playwright suite in Chromium, Firefox, and WebKit.
- Keep Playwright focused on critical user journeys.
- Build the frontend and API once before starting browser workers.
- Give each parallel Playwright worker its own API process, PostgreSQL schema,
  fake authentication, and controlled Immich fixture.
- Keep tests independent and avoid relying on execution order or shared
  installation state.
