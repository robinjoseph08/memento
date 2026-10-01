import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  window.localStorage.clear();
  window.history.replaceState(null, "", "/");
});

const returnTo = "memento://sign-in";
const alex = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  onboarding_completed_at: "2026-02-01T00:00:00Z",
  update_email: "alex@example.test",
  email_updates: false,
  avatar_url: "",
};

type Call = { path: string; body: Record<string, string> };

// serve answers the sign-in routes; verify decides what a code does.
function serve({
  codes = true,
  authMode = "google",
  send = () => new Response(null, { status: 204 }),
  verify = () => Response.json({ outcome: "signed_in", person: alex }),
}: {
  codes?: boolean;
  authMode?: string;
  send?: () => Response;
  verify?: (body: Record<string, string>) => Response;
} = {}) {
  const calls: Call[] = [];
  let person: typeof alex | null = null;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      const body = options?.body
        ? (JSON.parse(String(options.body)) as Record<string, string>)
        : {};
      calls.push({ path, body });
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person,
          auth_mode: authMode,
          sign_in_codes: codes,
          version: "test",
        });
      if (path === "/api/identity/sign-in-code") return send();
      if (path === "/api/identity/sign-in-code/verify") {
        const response = verify(body);
        const result = (await response.clone().json()) as {
          person?: typeof alex;
        };
        if (result.person) person = result.person;
        return response;
      }
      if (path === "/api/albums") return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  return calls;
}

it("signs in with an emailed code and keeps the Mobile App's return link", async () => {
  const calls = serve();
  window.history.replaceState(null, "", `/sign-in?return_to=${returnTo}`);
  const user = userEvent.setup();
  render(<App />);

  // Google stays below the email step as an alternative.
  expect(
    await screen.findByRole("link", { name: "Continue with Google" }),
  ).toBeVisible();
  await user.type(
    screen.getByRole("textbox", { name: "Email address" }),
    "Alex@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));

  expect(await screen.findByText(/We sent a code to/)).toHaveTextContent(
    "We sent a code to Alex@example.test",
  );
  expect(
    screen.queryByRole("link", { name: "Continue with Google" }),
  ).not.toBeInTheDocument();
  const code = screen.getByRole("textbox", { name: "Sign-in code" });
  expect(code).toHaveAttribute("autocomplete", "one-time-code");
  expect(code).toHaveAttribute("inputmode", "numeric");
  expect(code).toHaveFocus();
  expect(screen.getByRole("button", { name: "Resend code" })).toBeDisabled();

  // A pasted code with a space in it is enough to sign in.
  await user.paste("123 456");

  expect(await screen.findByRole("status")).toHaveTextContent(
    "Opening the Memento app…",
  );
  expect(
    screen.getByRole("link", { name: "Open the Memento app" }),
  ).toHaveAttribute(
    "href",
    `/api/identity/mobile/return?return_to=${encodeURIComponent(returnTo)}`,
  );
  expect(calls.filter((call) => call.path.includes("sign-in-code"))).toEqual([
    {
      path: "/api/identity/sign-in-code",
      body: { email: "Alex@example.test" },
    },
    {
      path: "/api/identity/sign-in-code/verify",
      body: { email: "Alex@example.test", code: "123456", display_name: "" },
    },
  ]);
  expect(window.localStorage.getItem("memento-sign-in-email")).toBe(
    "Alex@example.test",
  );
});

it("remembers the last address typed", async () => {
  window.localStorage.setItem("memento-sign-in-email", "alex@example.test");
  serve();
  window.history.replaceState(null, "", "/sign-in");
  render(<App />);
  expect(
    await screen.findByRole("textbox", { name: "Email address" }),
  ).toHaveValue("alex@example.test");
});

it("asks an unknown address for a name and sends the request to the Curator", async () => {
  const calls = serve({
    verify: (body) =>
      Response.json({
        outcome: body.display_name ? "requested" : "name_required",
        person: null,
      }),
  });
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup();
  render(<App />);

  await user.type(
    await screen.findByRole("textbox", { name: "Email address" }),
    "jordan@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await user.type(
    await screen.findByRole("textbox", { name: "Sign-in code" }),
    "654321",
  );

  expect(
    await screen.findByRole("heading", { name: "Request access" }),
  ).toBeVisible();
  await waitFor(() =>
    expect(screen.getByRole("textbox", { name: "Your name" })).toHaveFocus(),
  );
  await user.keyboard("Jordan");
  await user.click(screen.getByRole("button", { name: "Request" }));

  expect(await screen.findByRole("status")).toHaveTextContent(
    "Your Curator has been asked to review your request.",
  );
  expect(calls.at(-1)).toEqual({
    path: "/api/identity/sign-in-code/verify",
    body: {
      email: "jordan@example.test",
      code: "654321",
      display_name: "Jordan",
    },
  });
});

it("shows a wrong code beside the field and lets the Person start over", async () => {
  serve({
    verify: () =>
      Response.json(
        {
          error: {
            code: "validation_error",
            message: "Check the highlighted fields.",
            status_code: 422,
            fields: {
              code: "That code isn't right. Check the email and try again.",
            },
          },
        },
        { status: 422 },
      ),
  });
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup();
  render(<App />);
  await user.type(
    await screen.findByRole("textbox", { name: "Email address" }),
    "alex@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await user.type(
    await screen.findByRole("textbox", { name: "Sign-in code" }),
    "111111",
  );
  expect(await screen.findByText(/That code isn't right/)).toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Sign-in code" })).toHaveAttribute(
    "aria-invalid",
    "true",
  );

  await user.click(
    screen.getByRole("button", { name: "Use a different email" }),
  );
  const email = await screen.findByRole("textbox", { name: "Email address" });
  expect(email).toHaveValue("alex@example.test");
  await waitFor(() => expect(email).toHaveFocus());
});

it("keeps an earlier sign-in failure with the first step only", async () => {
  serve();
  window.history.replaceState(
    null,
    "",
    `/sign-in?error=access_denied&return_to=${returnTo}`,
  );
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This email address does not have access",
  );
  await user.type(
    screen.getByRole("textbox", { name: "Email address" }),
    "alex@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByRole("textbox", { name: "Sign-in code" });
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  const search = new URLSearchParams(window.location.search);
  expect(search.get("error")).toBeNull();
  expect(search.get("return_to")).toBe(returnTo);

  // Starting over does not bring the old failure back.
  await user.click(
    screen.getByRole("button", { name: "Use a different email" }),
  );
  await screen.findByRole("textbox", { name: "Email address" });
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("returns to the code step when the code dies while the name is typed", async () => {
  serve({
    verify: (body) =>
      body.display_name
        ? Response.json(
            {
              error: {
                code: "validation_error",
                message: "Check the highlighted fields.",
                status_code: 422,
                fields: {
                  code: "This code no longer works. Send a new code.",
                },
              },
            },
            { status: 422 },
          )
        : Response.json({ outcome: "name_required", person: null }),
  });
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup();
  render(<App />);
  await user.type(
    await screen.findByRole("textbox", { name: "Email address" }),
    "jordan@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await user.type(
    await screen.findByRole("textbox", { name: "Sign-in code" }),
    "654321",
  );
  await user.type(
    await screen.findByRole("textbox", { name: "Your name" }),
    "Jordan",
  );
  await user.click(screen.getByRole("button", { name: "Request" }));
  expect(
    await screen.findByRole("textbox", { name: "Sign-in code" }),
  ).toHaveAttribute("aria-invalid", "true");
  expect(screen.getByText(/This code no longer works/)).toBeInTheDocument();
});

it("says so when the code could not be sent", async () => {
  serve({
    send: () =>
      Response.json(
        {
          error: {
            code: "sign_in_code_unsent",
            message: "The sign-in code email could not be sent.",
            status_code: 503,
          },
        },
        { status: 503 },
      ),
  });
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup();
  render(<App />);
  await user.type(
    await screen.findByRole("textbox", { name: "Email address" }),
    "alex@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "We couldn't send your code. Try again in a minute.",
  );
  expect(
    screen.getByRole("textbox", { name: "Email address" }),
  ).toBeInTheDocument();
});

it("enables Resend after a minute", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const calls = serve({
    verify: () => Response.json({ outcome: "name_required", person: null }),
  });
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  render(<App />);
  await user.type(
    await screen.findByRole("textbox", { name: "Email address" }),
    "alex@example.test",
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
  const resend = await screen.findByRole("button", { name: "Resend code" });
  expect(resend).toBeDisabled();
  act(() => {
    vi.advanceTimersByTime(60_000);
  });
  expect(resend).toBeEnabled();
  await user.click(resend);
  expect(await screen.findByRole("status")).toHaveTextContent(
    "We sent a new code.",
  );
  expect(screen.getByRole("textbox", { name: "Sign-in code" })).toHaveFocus();
  expect(
    calls.filter((call) => call.path === "/api/identity/sign-in-code"),
  ).toHaveLength(2);
  expect(screen.getByRole("button", { name: "Resend code" })).toBeDisabled();
});

it("shows only the development form when mail is not configured", async () => {
  serve({ codes: false, authMode: "fake" });
  window.history.replaceState(null, "", "/sign-in");
  render(<App />);
  expect(
    await screen.findByRole("form", { name: "Fake development sign-in" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("textbox", { name: "Email address" }),
  ).not.toBeInTheDocument();
});
