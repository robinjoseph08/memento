import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { App } from "./App";

const contentionWait = { timeout: 5_000 };

function renderApp() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <BrowserRouter>
      <QueryClientProvider client={client}>
        <App />
      </QueryClientProvider>
    </BrowserRouter>,
  );
}

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") {
    return input;
  }
  if (input instanceof URL) {
    return input.href;
  }
  return input.url;
}

function stringBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") {
    throw new Error("Expected a JSON string request body");
  }
  return body;
}

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
  vi.restoreAllMocks();
});

test("opens an Invitation read-only and removes its token only after explicit acceptance", async () => {
  const token = "a".repeat(64);
  window.history.replaceState(null, "", `/invitation?token=${token}`);
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  let resolveAcceptance: (response: Response) => void = () => undefined;
  const acceptance = new Promise<Response>((resolve) => {
    resolveAcceptance = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/auth/invitations/inspect" && !init?.method) {
        return Promise.resolve(
          jsonResponse({
            recipient_name: "Alex",
            curator_name: "Robin",
            expires_at: "2026-08-10T12:00:00Z",
          }),
        );
      }
      if (path === "/api/auth/invitations/accept") {
        return acceptance;
      }
      if (path === "/api/session") {
        return new Promise<Response>(() => undefined);
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  await screen.findByRole("button", { name: "Accept Invitation" });
  expect(screen.getByText(/invited/, { selector: ".lede" })).toHaveTextContent(
    "Robin invited Alex to Memento.",
  );
  expect(window.location.search).toBe(`?token=${token}`);
  expect(requests).toHaveLength(1);
  expect(requests[0]).toMatchObject({
    path: "/api/auth/invitations/inspect",
    init: { headers: { "X-Memento-Invitation": token } },
  });
  expect(
    requests.some(
      ({ path }) => path === "/api/setup" || path === "/api/session",
    ),
  ).toBe(false);

  fireEvent.click(screen.getByRole("button", { name: "Accept Invitation" }));
  expect(window.location.search).toBe(`?token=${token}`);
  expect(screen.queryByText("Invitation accepted.")).not.toBeInTheDocument();
  resolveAcceptance(jsonResponse({ status: "onboarding" }));
  expect(await screen.findByText("Invitation accepted.")).toBeInTheDocument();
  expect(window.location.pathname).toBe("/");
  expect(window.location.search).toBe("");
  expect(requests[1]).toMatchObject({
    path: "/api/auth/invitations/accept",
    init: { method: "POST" },
  });
  expect(JSON.parse(stringBody(requests[1].init?.body))).toEqual({ token });
});

test("keeps a failed Invitation token available for an explicit retry", async () => {
  const token = "b".repeat(64);
  window.history.replaceState(null, "", `/invitation?token=${token}`);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/auth/invitations/inspect") {
        return Promise.resolve(
          jsonResponse({
            recipient_name: "Alex",
            curator_name: "Robin",
            expires_at: "2026-08-10T12:00:00Z",
          }),
        );
      }
      if (path === "/api/auth/invitations/accept") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Please retry." } }, 503),
        );
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  fireEvent.click(
    await screen.findByRole("button", { name: "Accept Invitation" }),
  );

  expect(await screen.findByRole("alert")).toHaveTextContent("Please retry.");
  expect(window.location.pathname).toBe("/invitation");
  expect(window.location.search).toBe(`?token=${token}`);
  expect(
    screen.getByRole("button", { name: "Accept Invitation" }),
  ).toBeEnabled();
});

test("recovers Onboarding after Session confirmation fails without retrying acceptance", async () => {
  const token = "e".repeat(64);
  const csrfToken = "f".repeat(64);
  let acceptanceCalls = 0;
  let sessionCalls = 0;
  window.history.replaceState(null, "", `/invitation?token=${token}`);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/auth/invitations/inspect") {
        return Promise.resolve(
          jsonResponse({
            recipient_name: "Alex",
            curator_name: "Robin",
            expires_at: "2026-08-10T12:00:00Z",
          }),
        );
      }
      if (path === "/api/auth/invitations/accept") {
        acceptanceCalls += 1;
        return Promise.resolve(jsonResponse({ status: "onboarding" }));
      }
      if (path === "/api/session") {
        sessionCalls += 1;
        return Promise.resolve(
          sessionCalls <= 3
            ? jsonResponse({ error: { message: "Session unavailable." } }, 503)
            : jsonResponse({
                display_name: "Alex",
                session_type: "public",
                csrf_token: csrfToken,
                curator: false,
                onboarding_required: true,
              }),
        );
      }
      if (path === "/api/onboarding") {
        return Promise.resolve(
          jsonResponse({
            status: "onboarding",
            recipient_name: "Alex",
            privacy_acknowledged: false,
            engagement_acknowledged: false,
            interest_list_acknowledged: false,
            email_previews_acknowledged: false,
            push_guidance_acknowledged: false,
            email_preference: "immediate",
            session_type: "",
            csrf_token: csrfToken,
          }),
        );
      }
      if (path === "/api/me/interest-list") {
        return Promise.resolve(
          jsonResponse({
            recipient: {
              id: "11111111-1111-4111-8111-111111111111",
              display_name: "Alex",
              sort_name: "alex",
            },
            version: 0,
            entries: [],
            history: [],
          }),
        );
      }
      if (path.startsWith("/api/me/people?")) {
        return Promise.resolve(jsonResponse({ people: [] }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  fireEvent.click(
    await screen.findByRole("button", { name: "Accept Invitation" }),
  );
  expect(
    await screen.findByRole("button", { name: "Open Onboarding again" }),
  ).toBeInTheDocument();
  expect(acceptanceCalls).toBe(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Open Onboarding again" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Welcome, Alex" }),
  ).toBeInTheDocument();
  expect(acceptanceCalls).toBe(1);
  expect(window.location.search).toBe("");
});

test("hands accepted identity to Onboarding and leaves the consumed Invitation after sign-out", async () => {
  const token = "c".repeat(64);
  const csrfToken = "d".repeat(64);
  let signedOut = false;
  window.history.replaceState(null, "", `/invitation?token=${token}`);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/auth/invitations/inspect") {
        return Promise.resolve(
          jsonResponse({
            recipient_name: "Alex",
            curator_name: "Robin",
            expires_at: "2026-08-10T12:00:00Z",
          }),
        );
      }
      if (path === "/api/auth/invitations/accept") {
        return Promise.resolve(jsonResponse({ status: "onboarding" }));
      }
      if (path === "/api/setup") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      }
      if (path === "/api/session/logout") {
        signedOut = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/session") {
        return Promise.resolve(
          signedOut
            ? jsonResponse({ error: { message: "Sign in required." } }, 401)
            : jsonResponse({
                display_name: "Alex",
                session_type: "public",
                csrf_token: csrfToken,
                curator: false,
                onboarding_required: true,
              }),
        );
      }
      if (path === "/api/onboarding") {
        return Promise.resolve(
          jsonResponse({
            status: "onboarding",
            recipient_name: "Alex",
            privacy_acknowledged: false,
            engagement_acknowledged: false,
            interest_list_acknowledged: false,
            email_previews_acknowledged: false,
            push_guidance_acknowledged: false,
            email_preference: "immediate",
            session_type: "",
            csrf_token: csrfToken,
          }),
        );
      }
      if (path === "/api/me/interest-list") {
        return Promise.resolve(
          jsonResponse({
            recipient: {
              id: "11111111-1111-4111-8111-111111111111",
              display_name: "Alex",
              sort_name: "alex",
            },
            version: 0,
            entries: [],
            history: [],
          }),
        );
      }
      if (path.startsWith("/api/me/people?")) {
        return Promise.resolve(jsonResponse({ people: [] }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  fireEvent.click(
    await screen.findByRole("button", { name: "Accept Invitation" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Welcome, Alex" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Your Interest list" }),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  expect(await screen.findByText("Setup is complete.")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Accept Invitation" }),
  ).not.toBeInTheDocument();
});

test("restores, saves, and explicitly completes Recipient Onboarding", async () => {
  const oldCSRF = "o".repeat(64);
  const newCSRF = "n".repeat(64);
  let completed = false;
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/setup") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      }
      if (path === "/api/session") {
        return Promise.resolve(
          jsonResponse({
            display_name: "Alex",
            session_type: completed ? "trusted" : "public",
            csrf_token: completed ? newCSRF : oldCSRF,
            curator: false,
            onboarding_required: !completed,
          }),
        );
      }
      if (path === "/api/onboarding" && init?.method === "PATCH") {
        return Promise.resolve(
          jsonResponse({
            status: "onboarding",
            recipient_name: "Alex",
            ...JSON.parse(stringBody(init.body)),
            csrf_token: oldCSRF,
          }),
        );
      }
      if (path === "/api/onboarding") {
        return Promise.resolve(
          jsonResponse({
            status: "onboarding",
            recipient_name: "Alex",
            privacy_acknowledged: true,
            engagement_acknowledged: false,
            interest_list_acknowledged: false,
            email_previews_acknowledged: false,
            push_guidance_acknowledged: false,
            email_preference: "weekly",
            session_type: "",
            csrf_token: oldCSRF,
          }),
        );
      }
      if (path === "/api/onboarding/complete") {
        completed = true;
        return Promise.resolve(
          jsonResponse({ status: "complete", csrf_token: newCSRF }),
        );
      }
      if (path === "/api/me/interest-list") {
        return Promise.resolve(
          jsonResponse({
            recipient: {
              id: "11111111-1111-4111-8111-111111111111",
              display_name: "Alex",
              sort_name: "alex",
            },
            version: 0,
            entries: [],
            history: [],
          }),
        );
      }
      if (path.startsWith("/api/me/people?")) {
        return Promise.resolve(jsonResponse({ people: [] }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  expect(
    await screen.findByRole("heading", { name: "Welcome, Alex" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText(/Private individual access/)).toBeChecked();
  expect(screen.getByLabelText("Publication and Comment email")).toHaveValue(
    "weekly",
  );
  expect(screen.getByText(/Media, search, Comments, archives/)).toBeVisible();
  expect(
    screen.getByRole("radio", { name: /Trusted device, stays signed in/ }),
  ).not.toBeChecked();
  expect(
    screen.getByRole("radio", { name: /Public computer, expires/ }),
  ).not.toBeChecked();
  expect(
    screen.getByRole("heading", { name: "Your Interest list" }),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText(/Curator-visible engagement/));
  fireEvent.click(
    screen.getByLabelText(/Interest choices suggest, never authorize/),
  );
  fireEvent.click(screen.getByLabelText(/Private email previews/));
  fireEvent.click(
    screen.getByLabelText(/Push is optional and device-specific/),
  );
  fireEvent.click(
    screen.getByRole("radio", { name: /Trusted device, stays signed in/ }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Save and continue later" }),
  );
  expect(
    await screen.findByText("Your Onboarding choices were saved."),
  ).toBeInTheDocument();
  const saveRequest = requests.find(
    ({ path, init }) => path === "/api/onboarding" && init?.method === "PATCH",
  );
  expect(saveRequest?.init?.headers).toMatchObject({
    "X-Memento-CSRF": oldCSRF,
  });
  expect(JSON.parse(stringBody(saveRequest?.init?.body))).toMatchObject({
    privacy_acknowledged: true,
    engagement_acknowledged: true,
    interest_list_acknowledged: true,
    email_previews_acknowledged: true,
    push_guidance_acknowledged: true,
    email_preference: "weekly",
    session_type: "trusted",
  });
  fireEvent.change(screen.getByLabelText("Publication and Comment email"), {
    target: { value: "immediate" },
  });
  expect(
    screen.queryByText("Your Onboarding choices were saved."),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Publication and Comment email"), {
    target: { value: "weekly" },
  });

  fireEvent.click(screen.getByRole("button", { name: "Complete Onboarding" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Welcome, Alex" }),
    ).not.toBeInTheDocument(),
  );
  const completeRequest = requests.find(
    ({ path }) => path === "/api/onboarding/complete",
  );
  expect(completeRequest?.init?.headers).toMatchObject({
    "X-Memento-CSRF": oldCSRF,
  });
  expect(JSON.parse(stringBody(completeRequest?.init?.body))).toEqual({
    privacy_acknowledged: true,
    engagement_acknowledged: true,
    interest_list_acknowledged: true,
    email_previews_acknowledged: true,
    push_guidance_acknowledged: true,
    email_preference: "weekly",
    session_type: "trusted",
  });
  expect(requests.filter(({ path }) => path === "/api/session")).toHaveLength(
    1,
  );
});

test("shows an unavailable Invitation instead of waiting forever when the token is absent", async () => {
  window.history.replaceState(null, "", "/invitation");
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);

  renderApp();

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Invitation is invalid or no longer available.",
  );
  expect(
    screen.queryByText(/Checking this Invitation/),
  ).not.toBeInTheDocument();
  expect(fetchMock).not.toHaveBeenCalled();
});

test("completes the first-browser setup workflow with explicit Onboarding choices", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/setup" && !init?.method) {
        return Promise.resolve(jsonResponse({ status: "available" }));
      }
      if (path === "/api/setup/code") {
        return Promise.resolve(
          jsonResponse({ challenge_id: "a".repeat(64), status: "queued" }, 202),
        );
      }
      if (path === "/api/setup/verify") {
        return Promise.resolve(
          jsonResponse({
            verification_token: "b".repeat(64),
            status: "verified",
          }),
        );
      }
      if (path === "/api/setup/complete") {
        return Promise.resolve(
          jsonResponse({ status: "complete", csrf_token: "c".repeat(64) }, 201),
        );
      }
      if (path === "/api/session") {
        return Promise.resolve(
          jsonResponse({
            display_name: "Robin Joseph",
            session_type: "public",
            csrf_token: "c".repeat(64),
            curator: true,
          }),
        );
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();

  fireEvent.change(await screen.findByLabelText("Your name"), {
    target: { value: "Robin Joseph" },
  });
  fireEvent.change(screen.getByLabelText("Login email"), {
    target: { value: "robin@example.com" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Send verification code" }),
  );

  expect(
    await screen.findByRole("heading", { name: "Verify your email" }),
  ).toHaveFocus();
  fireEvent.change(screen.getByLabelText("Verification code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify email" }));

  expect(
    await screen.findByRole("heading", {
      name: "Choose how Memento works for you",
    }),
  ).toHaveFocus();
  fireEvent.click(screen.getByLabelText(/Private individual access/));
  fireEvent.click(screen.getByLabelText(/Curator-visible engagement/));
  fireEvent.click(screen.getByLabelText(/Interest list starts empty/));
  fireEvent.click(screen.getByLabelText(/Private email previews/));
  fireEvent.click(screen.getByLabelText(/Push is an optional device choice/));
  fireEvent.change(screen.getByLabelText("Publication and Comment email"), {
    target: { value: "weekly" },
  });
  fireEvent.click(
    screen.getByRole("radio", { name: /Public computer, expires/ }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Complete setup" }));

  expect(
    await screen.findByText(
      "Setup is complete. You're signed in as Robin Joseph.",
    ),
  ).toBeInTheDocument();

  const requestCall = requests.find(({ path }) => path === "/api/setup/code");
  expect(JSON.parse(stringBody(requestCall?.init?.body))).toEqual({
    display_name: "Robin Joseph",
    email: "robin@example.com",
  });
  const completionCall = requests.find(
    ({ path }) => path === "/api/setup/complete",
  );
  expect(JSON.parse(stringBody(completionCall?.init?.body))).toEqual({
    verification_token: "b".repeat(64),
    privacy_acknowledged: true,
    engagement_acknowledged: true,
    interest_list_acknowledged: true,
    email_previews_acknowledged: true,
    push_guidance_acknowledged: true,
    email_preference: "weekly",
    session_type: "public",
  });
  expect(completionCall?.init?.credentials).toBe("same-origin");
  expect(completionCall?.init?.headers).toMatchObject({
    "Content-Type": "application/json",
  });
}, 10_000);

test("can queue another code or restart identity entry", async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const path = requestPath(input);
    if (path === "/api/setup") {
      return Promise.resolve(jsonResponse({ status: "available" }));
    }
    return Promise.resolve(
      jsonResponse({ challenge_id: "a".repeat(64), status: "queued" }, 202),
    );
  });
  vi.stubGlobal("fetch", fetchMock);

  renderApp();
  fireEvent.change(await screen.findByLabelText("Your name"), {
    target: { value: "Retry Person" },
  });
  fireEvent.change(screen.getByLabelText("Login email"), {
    target: { value: "retry@example.com" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Send verification code" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Send another code" }),
  );
  await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
  fireEvent.click(screen.getByRole("button", { name: "Change name or email" }));
  expect(await screen.findByLabelText("Your name")).toHaveValue("Retry Person");
});

test("does not claim sign-in until the Secure cookie restores a Session", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/setup") {
        return Promise.resolve(jsonResponse({ status: "available" }));
      }
      if (path === "/api/setup/code") {
        return Promise.resolve(
          jsonResponse({ challenge_id: "a".repeat(64), status: "queued" }, 202),
        );
      }
      if (path === "/api/setup/verify") {
        return Promise.resolve(
          jsonResponse({
            verification_token: "b".repeat(64),
            status: "verified",
          }),
        );
      }
      if (path === "/api/setup/complete") {
        return Promise.resolve(
          jsonResponse({ status: "complete", csrf_token: "c".repeat(64) }, 201),
        );
      }
      return Promise.resolve(
        jsonResponse(
          { error: { message: "A valid Session is required." } },
          401,
        ),
      );
    }),
  );

  renderApp();
  fireEvent.change(await screen.findByLabelText("Your name"), {
    target: { value: "Cookie Lost" },
  });
  fireEvent.change(screen.getByLabelText("Login email"), {
    target: { value: "cookie-lost@example.com" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Send verification code" }),
  );
  fireEvent.change(await screen.findByLabelText("Verification code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify email" }));
  fireEvent.click(await screen.findByLabelText(/Private individual access/));
  fireEvent.click(screen.getByLabelText(/Curator-visible engagement/));
  fireEvent.click(screen.getByLabelText(/Interest list starts empty/));
  fireEvent.click(screen.getByLabelText(/Private email previews/));
  fireEvent.click(screen.getByLabelText(/Push is an optional device choice/));
  fireEvent.click(screen.getByRole("button", { name: "Complete setup" }));

  expect(
    await screen.findByText("A valid Session is required."),
  ).toHaveAttribute("role", "alert");
  expect(screen.queryByText(/You're signed in/)).not.toBeInTheDocument();
});

test("distinguishes a reachable empty library from bootstrap failure and retries", async () => {
  let networkAvailable = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (!networkAvailable) {
        return Promise.reject(new TypeError("network unavailable"));
      }
      if (path === "/api/setup") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      }
      if (path === "/api/session") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Sign in required." } }, 401),
        );
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Memento cannot reach the server. Your authorized library has not been reported as empty.",
  );
  expect(
    screen.queryByText("No photos are available."),
  ).not.toBeInTheDocument();

  networkAvailable = true;
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(
    await screen.findByRole("heading", { name: "Sign in to Memento" }),
  ).toBeInTheDocument();
});

test("safe bootstrap GETs show permanent closure without starting setup", async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    expect(init?.method).toBeUndefined();
    if (requestPath(input) === "/api/setup") {
      return Promise.resolve(
        jsonResponse({ error: { message: "Setup not found." } }, 404),
      );
    }
    return Promise.resolve(
      jsonResponse({ error: { message: "A valid Session is required." } }, 401),
    );
  });
  vi.stubGlobal("fetch", fetchMock);

  renderApp();

  expect(await screen.findByText("Setup is complete.")).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(screen.getByLabelText("Login email")).toBeInTheDocument();
});

test("restores and refreshes a signed-in Trusted-device Session", async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = requestPath(input);
    if (path === "/api/setup") {
      return Promise.resolve(
        jsonResponse({ error: { message: "Setup not found." } }, 404),
      );
    }
    if (path === "/api/session/refresh") {
      expect(init?.method).toBe("POST");
      expect(init?.headers).toMatchObject({
        "X-Memento-CSRF": "c".repeat(64),
      });
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    if (path.startsWith("/api/sources?")) {
      return Promise.resolve(jsonResponse({ albums: [], next_cursor: null }));
    }
    if (path.startsWith("/api/people")) {
      return Promise.resolve(jsonResponse({ people: [] }));
    }
    if (path === "/api/invitation-suggestions") {
      return Promise.resolve(jsonResponse({ suggestions: [] }));
    }
    if (path === "/api/invitation-suggestions/curator") {
      return Promise.resolve(jsonResponse({ suggestions: [] }));
    }
    if (path.startsWith("/api/relationships?")) {
      return Promise.resolve(jsonResponse({ relationships: [] }));
    }
    if (path === "/api/repairs") {
      return Promise.resolve(
        jsonResponse({
          person_candidates: [],
          media_candidates: [],
          unlinked_immich_people: [],
        }),
      );
    }
    if (path.startsWith("/api/visibility-circles?")) {
      return Promise.resolve(jsonResponse({ circles: [] }));
    }
    return Promise.resolve(
      jsonResponse({
        display_name: "Robin Joseph",
        session_type: "trusted",
        csrf_token: "c".repeat(64),
        curator: true,
      }),
    );
  });
  vi.stubGlobal("fetch", fetchMock);

  renderApp();

  expect(
    await screen.findByText(
      "Setup is complete. You're signed in as Robin Joseph.",
    ),
  ).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledTimes(14);
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/people?query=&include_archived=false",
    expect.objectContaining({ credentials: "same-origin" }),
  );
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/relationships?include_archived=false",
    expect.objectContaining({ credentials: "same-origin" }),
  );
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/visibility-circles?include_archived=false",
    expect.objectContaining({ credentials: "same-origin" }),
  );
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/sources?disposition=unreviewed&limit=50",
    expect.objectContaining({ credentials: "same-origin" }),
  );
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/repairs",
    expect.objectContaining({ credentials: "same-origin" }),
  );
});

test("routes a non-Curator Session to the Recipient library and self-service tools", async () => {
  const requests: string[] = [];
  window.history.replaceState(null, "", "/?workspace=drafts");
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === "/api/setup") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      }
      if (path === "/api/session") {
        return Promise.resolve(
          jsonResponse({
            display_name: "Recipient",
            session_type: "trusted",
            csrf_token: "c".repeat(64),
            curator: false,
          }),
        );
      }
      if (path === "/api/session/refresh") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path.startsWith("/api/me/photos?")) {
        return Promise.resolve(jsonResponse({ media: [], next_cursor: null }));
      }
      if (path === "/api/me/new-for-you") {
        return Promise.resolve(jsonResponse({ events: [] }));
      }
      if (path === "/api/sessions") {
        return Promise.resolve(jsonResponse({ sessions: [] }));
      }
      if (path === "/api/me/interest-list") {
        return Promise.resolve(
          jsonResponse({
            recipient: {
              id: "11111111-1111-4111-8111-111111111111",
              display_name: "Recipient",
              sort_name: "Recipient",
            },
            entries: [],
            history: [],
          }),
        );
      }
      if (path.startsWith("/api/me/people?")) {
        return Promise.resolve(jsonResponse({ people: [] }));
      }
      if (path === "/api/invitation-suggestions") {
        return Promise.resolve(jsonResponse({ suggestions: [] }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  expect(
    await screen.findByRole("heading", { name: "Photos" }),
  ).toBeInTheDocument();
  expect(
    await screen.findByRole("heading", { name: "Your Interest list" }),
  ).toBeInTheDocument();
  expect(screen.queryByText("Source albums")).not.toBeInTheDocument();
  expect(requests.some((path) => path.startsWith("/api/people?"))).toBe(false);
  expect(requests.some((path) => path.startsWith("/api/relationships?"))).toBe(
    false,
  );
  expect(
    requests.some((path) => path.startsWith("/api/visibility-circles?")),
  ).toBe(false);
  expect(requests.some((path) => path.startsWith("/api/sources?"))).toBe(false);
});

test("keeps sign-out available from the draft organization workspace", async () => {
  const csrfToken = "c".repeat(64);
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  window.history.replaceState(null, "", "/?workspace=drafts");
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/setup")
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      if (path === "/api/session")
        return Promise.resolve(
          jsonResponse({
            display_name: "Robin Joseph",
            session_type: "public",
            csrf_token: csrfToken,
            curator: true,
          }),
        );
      if (path === "/api/events")
        return Promise.resolve(jsonResponse({ events: [] }));
      if (path === "/api/session/logout")
        return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  expect(
    await screen.findByRole("heading", { name: "Organize drafts" }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  expect(await screen.findByText("Setup is complete.")).toBeInTheDocument();
  const logout = requests.find(({ path }) => path === "/api/session/logout");
  expect(logout?.init).toMatchObject({
    method: "POST",
    headers: { "X-Memento-CSRF": csrfToken },
  });
});

test("validates Immich and supports private Source album ignore and restore triage", async () => {
  const csrfToken = "c".repeat(64);
  const sourceID = "11111111-1111-4111-8111-111111111111";
  let disposition: "unreviewed" | "ignored" = "unreviewed";
  let version = 1;
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/setup") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      }
      if (path === "/api/session") {
        return Promise.resolve(
          jsonResponse({
            display_name: "Robin Joseph",
            session_type: "public",
            csrf_token: csrfToken,
            curator: true,
          }),
        );
      }
      if (path === "/api/sources/discover") {
        return Promise.resolve(
          jsonResponse({ status: "connected", discovered_count: 1 }),
        );
      }
      if (path === "/api/session/logout") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === `/api/sources/${sourceID}/reconcile`) {
        return Promise.resolve(jsonResponse({ status: "queued" }, 202));
      }
      if (path === `/api/sources/${sourceID}/ignore`) {
        expect(init?.headers).toMatchObject({ "If-Match": `"${version}"` });
        disposition = "ignored";
        version += 1;
        return Promise.resolve(jsonResponse(sourceAlbum(disposition)));
      }
      if (path === `/api/sources/${sourceID}/restore`) {
        expect(init?.headers).toMatchObject({ "If-Match": `"${version}"` });
        disposition = "unreviewed";
        version += 1;
        return Promise.resolve(jsonResponse(sourceAlbum(disposition)));
      }
      if (path.startsWith("/api/people")) {
        return Promise.resolve(jsonResponse({ people: [] }));
      }
      if (path === "/api/repairs") {
        return Promise.resolve(
          jsonResponse({
            person_candidates: [],
            media_candidates: [],
            unlinked_immich_people: [],
          }),
        );
      }
      if (path.startsWith("/api/sources?")) {
        const requestedDisposition = new URL(
          path,
          "https://memento.test",
        ).searchParams.get("disposition");
        return Promise.resolve(
          jsonResponse({
            albums:
              requestedDisposition === disposition
                ? [sourceAlbum(disposition)]
                : [],
            next_cursor: null,
          }),
        );
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  function sourceAlbum(state: "unreviewed" | "ignored") {
    return {
      id: sourceID,
      name: "Family trip",
      description: "A normalized summary",
      asset_count: 7,
      source_created_at: "2026-01-01T00:00:00Z",
      source_updated_at: "2026-02-01T00:00:00Z",
      start_at: "2026-01-01T00:00:00Z",
      end_at: "2026-01-07T00:00:00Z",
      disposition: state,
      version,
      first_seen_at: "2026-03-01T00:00:00Z",
      last_seen_at: "2026-03-02T00:00:00Z",
      source_missing: false,
    };
  }

  renderApp();
  expect(
    await screen.findByText("Family trip", {}, contentionWait),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Connect and discover" }));
  expect(
    await screen.findByText(
      "Immich v3.0.3 connected. Found 1 owned album.",
      {},
      contentionWait,
    ),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Inspect Family trip" }));
  expect(screen.getByText("A normalized summary")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));
  await vi.waitFor(
    () =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Queued reconciliation for Family trip.",
      ),
    contentionWait,
  );
  fireEvent.click(screen.getByRole("button", { name: "Ignore Source album" }));
  await vi.waitFor(
    () => expect(screen.queryByText("Family trip")).not.toBeInTheDocument(),
    contentionWait,
  );
  expect(screen.getByRole("status")).toHaveTextContent("Ignored Family trip.");
  fireEvent.click(screen.getByRole("button", { name: "Ignored" }));
  expect(window.location.search).toBe("?source_view=ignored");
  expect(
    await screen.findByText("Family trip", {}, contentionWait),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Inspect Family trip" }));
  fireEvent.click(screen.getByRole("button", { name: "Restore to inbox" }));
  await vi.waitFor(
    () => expect(screen.queryByText("Family trip")).not.toBeInTheDocument(),
    contentionWait,
  );

  const mutations = requests.filter(({ init }) => init?.method === "POST");
  expect(mutations).toHaveLength(4);
  for (const mutation of mutations) {
    expect(mutation.init?.headers).toMatchObject({
      "X-Memento-CSRF": csrfToken,
    });
    expect(mutation.path).not.toContain("Immich");
  }

  expect(screen.getByRole("status")).toHaveTextContent(
    "Restored Family trip to the Source album inbox.",
  );
  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  expect(
    await screen.findByText("Setup is complete.", {}, contentionWait),
  ).toBeInTheDocument();
  expect(screen.queryByText("Source albums")).not.toBeInTheDocument();
}, 15_000);

test("loads the next opaque Source album cursor without replacing prior results", async () => {
  const cursor = "opaque/cursor+value";
  const album = (id: string, name: string) => ({
    id,
    name,
    description: "",
    asset_count: 1,
    source_created_at: "2026-01-01T00:00:00Z",
    source_updated_at: "2026-01-01T00:00:00Z",
    start_at: null,
    end_at: null,
    disposition: "unreviewed",
    version: 1,
    first_seen_at: "2026-03-01T00:00:00Z",
    last_seen_at: "2026-03-01T00:00:00Z",
    source_missing: false,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/setup") {
        return Promise.resolve(
          jsonResponse({ error: { message: "Setup not found." } }, 404),
        );
      }
      if (path === "/api/session") {
        return Promise.resolve(
          jsonResponse({
            display_name: "Robin Joseph",
            session_type: "trusted",
            csrf_token: "c".repeat(64),
            curator: true,
          }),
        );
      }
      if (path === "/api/session/refresh") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path.startsWith("/api/people")) {
        return Promise.resolve(jsonResponse({ people: [] }));
      }
      if (path === "/api/repairs") {
        return Promise.resolve(
          jsonResponse({
            person_candidates: [],
            media_candidates: [],
            unlinked_immich_people: [],
          }),
        );
      }
      if (path.startsWith("/api/sources?")) {
        const requestedCursor = new URL(
          path,
          "https://memento.test",
        ).searchParams.get("cursor");
        return Promise.resolve(
          requestedCursor === cursor
            ? jsonResponse({
                albums: [
                  album("22222222-2222-4222-8222-222222222222", "Second"),
                ],
                next_cursor: null,
              })
            : jsonResponse({
                albums: [
                  album("11111111-1111-4111-8111-111111111111", "First"),
                ],
                next_cursor: cursor,
              }),
        );
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderApp();
  expect(await screen.findByText("First")).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Load more Source albums" }),
  );
  expect(await screen.findByText("Second")).toBeInTheDocument();
  expect(screen.getByText("First")).toBeInTheDocument();
});

test("announces a concurrent setup conflict without claiming success", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/setup") {
        return Promise.resolve(jsonResponse({ status: "available" }));
      }
      if (path === "/api/setup/code") {
        return Promise.resolve(
          jsonResponse({ challenge_id: "a".repeat(64), status: "queued" }, 202),
        );
      }
      if (path === "/api/setup/verify") {
        return Promise.resolve(
          jsonResponse({
            verification_token: "b".repeat(64),
            status: "verified",
          }),
        );
      }
      return Promise.resolve(
        jsonResponse(
          { error: { message: "Setup is no longer available." } },
          409,
        ),
      );
    }),
  );

  renderApp();
  fireEvent.change(await screen.findByLabelText("Your name"), {
    target: { value: "Losing Browser" },
  });
  fireEvent.change(screen.getByLabelText("Login email"), {
    target: { value: "loser@example.com" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Send verification code" }),
  );
  fireEvent.change(await screen.findByLabelText("Verification code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify email" }));
  fireEvent.click(await screen.findByLabelText(/Private individual access/));
  fireEvent.click(screen.getByLabelText(/Curator-visible engagement/));
  fireEvent.click(screen.getByLabelText(/Interest list starts empty/));
  fireEvent.click(screen.getByLabelText(/Private email previews/));
  fireEvent.click(screen.getByLabelText(/Push is an optional device choice/));
  fireEvent.click(screen.getByRole("button", { name: "Complete setup" }));

  expect(
    await screen.findByText("Setup is no longer available."),
  ).toHaveAttribute("role", "alert");
  expect(screen.queryByText(/You're signed in/)).not.toBeInTheDocument();
});
