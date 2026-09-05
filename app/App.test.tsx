import { focusManager } from "@tanstack/react-query";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { App } from "./App";

beforeEach(() => {
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
  focusManager.setFocused(undefined);
  delete document.documentElement.dataset.theme;
  window.history.replaceState(null, "", "/");
});

const curator = {
  id: "curator-id",
  display_name: "Local Curator",
  is_curator: true,
};

function serveIdentity(
  claimed = false,
  signInResponse?: () => Promise<Response>,
  connectionResponse?: () => Promise<Response>,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/sign-out"))
        return new Response(null, { status: 204 });
      if (path.endsWith("/status"))
        return Response.json({ claimed, person: null, auth_mode: "fake" });
      if (path.endsWith("/fake-sign-in")) {
        if (signInResponse) return signInResponse();
        const claims = JSON.parse(String(options?.body)) as {
          display_name: string;
        };
        return Response.json({ ...curator, display_name: claims.display_name });
      }
      if (path.endsWith("/connection"))
        return connectionResponse
          ? connectionResponse()
          : Response.json({
              usable: true,
              version: "2.0.0",
              message: "Connected to Immich.",
            });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
}

it("keeps normal routes behind setup while the installation is unclaimed", async () => {
  window.history.replaceState(null, "", "/curator");
  serveIdentity();
  render(<App />);

  expect(screen.getByRole("status")).toHaveTextContent("Loading memento");
  expect(
    await screen.findByRole("heading", { name: "Make room for your memories" }),
  ).toBeInTheDocument();
  expect(screen.getByText(/first successful sign-in/i)).toBeInTheDocument();
  expect(window.location.pathname).toBe("/setup");
});

it("claims the installation with the edited fake identity using native Enter submission", async () => {
  serveIdentity();
  const user = userEvent.setup();
  render(<App />);

  const name = await screen.findByRole("textbox", { name: "Display name" });
  await user.clear(name);
  await user.type(name, "Robin{Enter}");

  expect(
    await screen.findByRole("heading", { name: "Your albums" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Robin")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /import/i }),
  ).not.toBeInTheDocument();
  expect(window.location.pathname).toBe("/curator");
});

it("preserves entered claims and focuses the first field rejected by the server", async () => {
  serveIdentity(false, async () =>
    Response.json(
      {
        error: {
          message: "Check your details.",
          fields: {
            display_name: "Choose a display name.",
            subject: "Choose a different subject.",
          },
        },
      },
      { status: 400 },
    ),
  );
  const user = userEvent.setup();
  render(<App />);
  const subject = await screen.findByRole("textbox", { name: "Subject" });
  await user.clear(subject);
  await user.type(subject, "other-subject");
  await user.click(screen.getByRole("button", { name: "Claim installation" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Check your details.",
  );
  expect(subject).toHaveValue("other-subject");
  expect(subject).toHaveFocus();
  expect(subject).toHaveAccessibleDescription("Choose a different subject.");
  expect(
    screen.getByRole("textbox", { name: "Display name" }),
  ).toHaveAccessibleDescription("Choose a display name.");
});

it("allows claiming while Immich is unreachable", async () => {
  serveIdentity(false, undefined, async () =>
    Response.json({
      usable: false,
      version: "",
      message: "Immich could not be reached.",
    }),
  );
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByText("Immich could not be reached."),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Claim installation" }));
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeInTheDocument();
});

it("shows a failed Immich diagnostic and checks again only when requested", async () => {
  let usable = false;
  serveIdentity(false, undefined, async () =>
    Response.json({
      usable,
      version: usable ? "2.0.0" : "",
      message: usable
        ? "Connected to Immich."
        : "Immich could not be reached. Check its URL and try again.",
    }),
  );
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByText(
      "Immich could not be reached. Check its URL and try again.",
    ),
  ).toBeInTheDocument();
  usable = true;
  await user.click(screen.getByRole("button", { name: "Check again" }));
  expect(await screen.findByText("Connected to Immich.")).toBeInTheDocument();
  expect(screen.getByText("Version 2.0.0")).toBeInTheDocument();
});

it("signs out of the Curator shell and redirects a protected bookmark to sign-in", async () => {
  serveIdentity(true);
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  const view = render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Welcome back" }),
  ).toBeInTheDocument();
  expect(window.location.pathname).toBe("/sign-in");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(
    await screen.findByRole("heading", { name: "Your albums" }),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Sign out" }));
  expect(
    await screen.findByRole("heading", { name: "Welcome back" }),
  ).toBeInTheDocument();
  expect(window.location.pathname).toBe("/sign-in");
  view.unmount();
});

it("protects edited claims from navigation and reload without blocking successful sign-in", async () => {
  serveIdentity();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const user = userEvent.setup();
  render(<App />);
  const subject = await screen.findByRole("textbox", { name: "Subject" });
  await user.clear(subject);
  await user.type(subject, "keep-this-subject");
  await user.click(screen.getByRole("link", { name: "memento home" }));
  expect(confirm).toHaveBeenCalled();
  expect(subject).toHaveValue("keep-this-subject");
  expect(window.location.pathname).toBe("/setup");
  const beforeUnload = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(beforeUnload);
  expect(beforeUnload.defaultPrevented).toBe(true);
  confirm.mockClear();
  await user.click(screen.getByRole("button", { name: "Claim installation" }));
  expect(
    await screen.findByRole("heading", { name: "Your albums" }),
  ).toBeInTheDocument();
  expect(confirm).not.toHaveBeenCalled();
});

it("offers a retry after a server error without showing server details", async () => {
  let failed = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      failed
        ? Response.json(
            { error: { message: "secret database details" } },
            { status: 500 },
          )
        : Response.json({ claimed: false, person: null, auth_mode: "fake" }),
    ),
  );
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong. Please try again.",
  );
  expect(screen.queryByText("secret database details")).not.toBeInTheDocument();
  failed = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect(
    await screen.findByRole("heading", { name: "Make room for your memories" }),
  ).toBeInTheDocument();
});

it("disables the pending form and preserves an unknown identity after access is denied", async () => {
  let finish!: (response: Response) => void;
  const response = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  serveIdentity(true, () => response);
  const user = userEvent.setup();
  render(<App />);
  const subject = await screen.findByRole("textbox", { name: "Subject" });
  await user.clear(subject);
  await user.type(subject, "unknown-person{Enter}");
  expect(
    await screen.findByRole("button", { name: "Signing in…" }),
  ).toBeDisabled();
  expect(subject).toBeDisabled();
  await act(async () =>
    finish(
      Response.json(
        {
          error: {
            code: "access_denied",
            message: "This identity does not have access to this installation.",
            status_code: 403,
          },
        },
        { status: 403 },
      ),
    ),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This identity does not have access to this installation.",
  );
  expect(subject).toHaveValue("unknown-person");
  expect(subject).toBeEnabled();
  expect(window.location.pathname).toBe("/sign-in");
});

it("cancels an in-flight Curator diagnostic when signing out", async () => {
  let signal: AbortSignal | null | undefined;
  let finish!: (response: Response) => void;
  const response = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (path.endsWith("/connection")) {
        signal = options?.signal;
        return response;
      }
      return new Response(null, { status: 204 });
    }),
  );
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("heading", { name: "No albums yet" });
  await user.click(screen.getByRole("button", { name: "Sign out" }));
  await screen.findByRole("heading", { name: "Welcome back" });
  expect(signal?.aborted).toBe(true);
  await act(async () =>
    finish(
      Response.json({ usable: true, version: "2.7.0", message: "Connected" }),
    ),
  );
  expect(
    screen.getByRole("heading", { name: "Welcome back" }),
  ).toBeInTheDocument();
});

it("preserves edited sign-in fields through a failed background status refresh and retry", async () => {
  let failed = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return failed
          ? Response.json(
              { error: { message: "Unavailable" } },
              { status: 503 },
            )
          : Response.json({ claimed: true, person: null, auth_mode: "fake" });
      return Response.json({
        usable: true,
        version: "2.7.0",
        message: "Connected",
      });
    }),
  );
  const user = userEvent.setup();
  render(<App />);
  const subject = await screen.findByRole("textbox", { name: "Subject" });
  await user.clear(subject);
  await user.type(subject, "preserve-this-edit");
  failed = true;
  await act(async () => {
    focusManager.setFocused(false);
    vi.setSystemTime(Date.now() + 31_000);
    focusManager.setFocused(true);
  });
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Subject" })).toHaveValue(
    "preserve-this-edit",
  );
  failed = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect(screen.getByRole("textbox", { name: "Subject" })).toHaveValue(
    "preserve-this-edit",
  );
});

it("defaults to dark and remembers an explicit light theme across visits", async () => {
  serveIdentity();
  const user = userEvent.setup();
  const view = render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Switch to light theme" }),
  );
  expect(
    screen.getByRole("button", { name: "Switch to dark theme" }),
  ).toBeInTheDocument();
  view.unmount();
  render(<App />);
  expect(
    await screen.findByRole("button", { name: "Switch to dark theme" }),
  ).toBeInTheDocument();
});
