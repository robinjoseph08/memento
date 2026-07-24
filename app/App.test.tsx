import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { App } from "./App";

function renderApp() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>,
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
  vi.restoreAllMocks();
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
          jsonResponse(
            { challenge_id: "a".repeat(64), status: "code_sent" },
            202,
          ),
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

  fireEvent.change(await screen.findByLabelText("Verification code"), {
    target: { value: "12345678" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify email" }));

  fireEvent.click(await screen.findByLabelText(/Private individual access/));
  fireEvent.click(screen.getByLabelText(/Curator-visible engagement/));
  fireEvent.click(screen.getByLabelText(/Interest list starts empty/));
  fireEvent.change(screen.getByLabelText("Publication and Comment email"), {
    target: { value: "weekly" },
  });
  fireEvent.click(screen.getByLabelText(/Public computer/));
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
    email_preference: "weekly",
    session_type: "public",
  });
  expect(completionCall?.init?.credentials).toBe("same-origin");
  expect(completionCall?.init?.headers).toMatchObject({
    "Content-Type": "application/json",
  });
  expect(screen.getByRole("button", { name: "Sign out" })).toBeInTheDocument();
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

  expect(
    await screen.findByText(
      "Setup is complete. Memento is ready for private family sharing.",
    ),
  ).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(screen.queryByLabelText("Login email")).not.toBeInTheDocument();
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
    return Promise.resolve(
      jsonResponse({
        display_name: "Robin Joseph",
        session_type: "trusted",
        csrf_token: "c".repeat(64),
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
  expect(fetchMock).toHaveBeenCalledTimes(3);
});

test("keeps sign-out prominent and sends Session-bound CSRF", async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = requestPath(input);
    if (path === "/api/setup") {
      return Promise.resolve(
        jsonResponse({ error: { message: "Setup not found." } }, 404),
      );
    }
    if (path === "/api/session/logout") {
      expect(init?.method).toBe("POST");
      expect(init?.headers).toMatchObject({
        "X-Memento-CSRF": "c".repeat(64),
      });
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    return Promise.resolve(
      jsonResponse({
        display_name: "Public Person",
        session_type: "public",
        csrf_token: "c".repeat(64),
      }),
    );
  });
  vi.stubGlobal("fetch", fetchMock);

  renderApp();
  fireEvent.click(await screen.findByRole("button", { name: "Sign out" }));

  expect(
    await screen.findByText(
      "Setup is complete. Memento is ready for private family sharing.",
    ),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Sign out" }),
  ).not.toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledTimes(3);
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
          jsonResponse(
            { challenge_id: "a".repeat(64), status: "code_sent" },
            202,
          ),
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
  fireEvent.click(screen.getByRole("button", { name: "Complete setup" }));

  expect(
    await screen.findByText("Setup is no longer available."),
  ).toHaveAttribute("role", "alert");
  expect(screen.queryByText(/You're signed in/)).not.toBeInTheDocument();
});
