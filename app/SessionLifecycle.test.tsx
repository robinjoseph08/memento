import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { App } from "./App";

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderApp() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <BrowserRouter>
      <QueryClientProvider client={client}>
        <App />
      </QueryClientProvider>
    </BrowserRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("signs in with an eight-digit code and keeps Public-computer warnings prominent", async () => {
  let signedIn = false;
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      requests.push({ path, init });
      if (path === "/api/setup")
        return Promise.resolve(json({ error: { message: "not found" } }, 404));
      if (path === "/api/session")
        return Promise.resolve(
          signedIn
            ? json({
                display_name: "Alex",
                session_type: "public",
                csrf_token: "c".repeat(64),
                curator: false,
                onboarding_required: false,
              })
            : json({ error: { message: "sign in" } }, 401),
        );
      if (path === "/api/auth/sign-in/request")
        return Promise.resolve(
          json({ challenge_id: "a".repeat(64), status: "accepted" }, 202),
        );
      if (path === "/api/auth/sign-in/verify") {
        signedIn = true;
        return Promise.resolve(json({ status: "signed_in" }));
      }
      if (path.startsWith("/api/sessions/") && init?.method === "PATCH")
        return Promise.resolve(new Response(null, { status: 204 }));
      if (path.startsWith("/api/sessions/") && init?.method === "DELETE") {
        signedIn = false;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/sessions")
        return Promise.resolve(
          json({
            sessions: [
              {
                id: "11111111-1111-4111-8111-111111111111",
                label: "",
                browser: "Firefox",
                platform: "Linux",
                session_type: "public",
                created_at: "2026-07-27T12:00:00Z",
                last_activity_at: "2026-07-27T12:00:00Z",
                expires_at: "2026-07-28T00:00:00Z",
                status: "active",
                current: true,
                push_allowed: false,
              },
            ],
          }),
        );
      if (path === "/api/me/interest-list")
        return Promise.resolve(
          json({
            recipient: { id: "1", display_name: "Alex", sort_name: "alex" },
            version: 0,
            entries: [],
            history: [],
          }),
        );
      if (path.startsWith("/api/me/people?"))
        return Promise.resolve(json({ people: [] }));
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  fireEvent.change(await screen.findByLabelText("Login email"), {
    target: { value: "alex@example.com" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send sign-in code" }));
  fireEvent.change(await screen.findByLabelText("Sign-in code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(
    screen.getByRole("radio", { name: /Public computer, browser-session/ }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Verify and sign in" }));

  expect(
    await screen.findByText("Public computer", { selector: "strong" }),
  ).toBeVisible();
  expect(screen.getByText(/Push is disabled/)).toBeVisible();
  expect(
    screen.getByText(/downloaded originals or archives remain/),
  ).toBeVisible();
  fireEvent.click(screen.getByText("Sessions and login email"));
  expect(await screen.findByText("Push unavailable")).toBeVisible();
  expect(screen.getByText(/created .* last active/)).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Sign out Firefox on Linux" }),
  ).toBeVisible();
  fireEvent.change(screen.getByLabelText("Session name for Firefox on Linux"), {
    target: { value: "Shared laptop" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Save name for Firefox on Linux" }),
  );
  await vi.waitFor(() =>
    expect(
      requests.some(
        ({ path, init }) =>
          path === "/api/sessions/11111111-1111-4111-8111-111111111111" &&
          init?.method === "PATCH" &&
          (JSON.parse(init.body as string) as { label: string }).label ===
            "Shared laptop",
      ),
    ).toBe(true),
  );
  const verify = requests.find(
    ({ path }) => path === "/api/auth/sign-in/verify",
  );
  expect(JSON.parse(verify?.init?.body as string)).toEqual({
    challenge_id: "a".repeat(64),
    code: "12345678",
    session_type: "public",
  });

  fireEvent.click(
    screen.getByRole("button", { name: "Sign out Firefox on Linux" }),
  );
  fireEvent.change(await screen.findByLabelText("Login email"), {
    target: { value: "alex@example.com" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send sign-in code" }));
  fireEvent.change(await screen.findByLabelText("Sign-in code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify and sign in" }));
  expect(
    await screen.findByText("Public computer", { selector: "strong" }),
  ).toBeVisible();
  expect(
    requests.filter(({ path }) => path === "/api/auth/sign-in/verify"),
  ).toHaveLength(2);
});

test("retries Session bootstrap without reusing an accepted sign-in code", async () => {
  let signedIn = false;
  let bootstrapFailures = 1;
  let verificationCalls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      if (path === "/api/setup")
        return Promise.resolve(json({ error: { message: "not found" } }, 404));
      if (path === "/api/session") {
        if (!signedIn)
          return Promise.resolve(json({ error: { message: "sign in" } }, 401));
        if (bootstrapFailures > 0) {
          bootstrapFailures -= 1;
          return Promise.resolve(
            json(
              { error: { message: "Session temporarily unavailable" } },
              503,
            ),
          );
        }
        return Promise.resolve(
          json({
            display_name: "Alex",
            session_type: "trusted",
            csrf_token: "c".repeat(64),
            curator: false,
            onboarding_required: false,
          }),
        );
      }
      if (path === "/api/auth/sign-in/request")
        return Promise.resolve(
          json({ challenge_id: "a".repeat(64), status: "accepted" }, 202),
        );
      if (path === "/api/auth/sign-in/verify") {
        verificationCalls += 1;
        signedIn = true;
        return Promise.resolve(json({ status: "signed_in" }));
      }
      if (path === "/api/sessions")
        return Promise.resolve(json({ sessions: [] }));
      if (path === "/api/me/interest-list")
        return Promise.resolve(
          json({
            recipient: { id: "1", display_name: "Alex", sort_name: "alex" },
            version: 0,
            entries: [],
            history: [],
          }),
        );
      if (path.startsWith("/api/me/people?"))
        return Promise.resolve(json({ people: [] }));
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  fireEvent.change(await screen.findByLabelText("Login email"), {
    target: { value: "alex@example.com" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send sign-in code" }));
  fireEvent.change(await screen.findByLabelText("Sign-in code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify and sign in" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry loading Session" }),
  );
  expect(await screen.findByText("Your Interest list")).toBeVisible();
  expect(verificationCalls).toBe(1);
});
