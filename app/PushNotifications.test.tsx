import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { PushNotifications } from "./PushNotifications";
import type { SessionResponse } from "./types/generated/setup";

const trustedSession: SessionResponse = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

function json(value: unknown) {
  return Promise.resolve(
    new Response(JSON.stringify(value), {
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function requestPath(input: RequestInfo | URL) {
  return typeof input === "string"
    ? input
    : input instanceof URL
      ? input.href
      : input.url;
}

function renderPush(session = trustedSession) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <PushNotifications session={session} />
    </QueryClientProvider>,
  );
}

function installBrowserPush(options?: {
  permission?: NotificationPermission;
  userAgent?: string;
  standalone?: boolean;
  subscription?: PushSubscription | null;
}) {
  const subscription =
    options?.subscription === undefined ? null : options.subscription;
  const getSubscription = vi.fn(() => Promise.resolve(subscription));
  const subscribe = vi.fn<PushManager["subscribe"]>();
  const registration = {
    pushManager: { getSubscription, subscribe },
  } as unknown as ServiceWorkerRegistration;
  const serviceWorker = {
    ready: Promise.resolve(registration),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  };
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: serviceWorker,
  });
  Object.defineProperty(window, "isSecureContext", {
    configurable: true,
    value: true,
  });
  Object.defineProperty(navigator, "standalone", {
    configurable: true,
    value: options?.standalone ?? false,
  });
  vi.spyOn(navigator, "userAgent", "get").mockReturnValue(
    options?.userAgent ?? "Mozilla/5.0 (X11; Linux x86_64) Chrome/130",
  );
  const requestPermission = vi.fn<() => Promise<NotificationPermission>>(() =>
    Promise.resolve("granted"),
  );
  vi.stubGlobal("PushManager", class PushManager {});
  vi.stubGlobal("Notification", {
    permission: options?.permission ?? "default",
    requestPermission,
  });
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches: options?.standalone ?? false })),
  );
  return {
    getSubscription,
    registration,
    requestPermission,
    serviceWorker,
    subscribe,
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "serviceWorker");
  Reflect.deleteProperty(navigator, "standalone");
  Reflect.deleteProperty(window, "isSecureContext");
});

test("prefetches configuration and reconciles an authenticated launch without subscribing", async () => {
  const browser = installBrowserPush();
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/push")
        return json({ available: true, public_key: "AQID", enrolled: false });
      if (path === "/api/push/reconcile")
        return json({ enrolled: false, remove_local: false });
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderPush();

  expect(
    await screen.findByRole("button", { name: "Enable push notifications" }),
  ).toBeVisible();
  await waitFor(() => expect(browser.getSubscription).toHaveBeenCalledOnce());
  expect(browser.subscribe).not.toHaveBeenCalled();
  expect(browser.requestPermission).not.toHaveBeenCalled();
  const reconcile = requests.find(({ path }) => path === "/api/push/reconcile");
  expect(reconcile?.init).toMatchObject({
    method: "POST",
    headers: { "X-Memento-CSRF": trustedSession.csrf_token },
  });
  expect(JSON.parse(reconcile?.init?.body as string)).toEqual({});
});

test("requests permission synchronously from Enable before awaiting browser work", async () => {
  const browser = installBrowserPush();
  const localSubscription = {
    endpoint: "https://push.example/subscription",
    toJSON: () => ({
      endpoint: "https://push.example/subscription",
      expirationTime: null,
      keys: { p256dh: "public-key", auth: "auth-secret" },
    }),
  } as unknown as PushSubscription;
  browser.subscribe.mockResolvedValue(localSubscription);
  let resolvePermission: (permission: NotificationPermission) => void = () =>
    undefined;
  const permission = new Promise<NotificationPermission>((resolve) => {
    resolvePermission = resolve;
  });
  browser.requestPermission.mockReturnValue(permission);
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/push" && init?.method === "POST")
        return json({ available: true, public_key: "AQID", enrolled: true });
      if (path === "/api/push")
        return json({ available: true, public_key: "AQID", enrolled: false });
      if (path === "/api/push/reconcile")
        return json({ enrolled: false, remove_local: false });
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderPush();
  const button = await screen.findByRole("button", {
    name: "Enable push notifications",
  });
  await waitFor(() => expect(browser.getSubscription).toHaveBeenCalledOnce());
  browser.getSubscription.mockClear();
  fireEvent.click(button);

  expect(browser.requestPermission).toHaveBeenCalledOnce();
  expect(browser.getSubscription).not.toHaveBeenCalled();
  expect(browser.subscribe).not.toHaveBeenCalled();

  resolvePermission("granted");
  expect(
    await screen.findByText("Push notifications are enabled on this device."),
  ).toBeVisible();
  expect(browser.getSubscription).toHaveBeenCalledOnce();
  expect(browser.subscribe).toHaveBeenCalledWith({
    userVisibleOnly: true,
    applicationServerKey: new Uint8Array([1, 2, 3]),
  });
  const enrollment = requests.find(
    ({ path, init }) => path === "/api/push" && init?.method === "POST",
  );
  expect(JSON.parse(enrollment?.init?.body as string)).toEqual({
    endpoint: "https://push.example/subscription",
    keys: { p256dh: "public-key", auth: "auth-secret" },
  });
});

test("an active enrollment can be disabled and unsubscribed locally", async () => {
  const unsubscribe = vi.fn(() => Promise.resolve(true));
  const localSubscription = {
    endpoint: "https://push.example/subscription",
    toJSON: () => ({
      endpoint: "https://push.example/subscription",
      expirationTime: null,
      keys: { p256dh: "public-key", auth: "auth-secret" },
    }),
    unsubscribe,
  } as unknown as PushSubscription;
  installBrowserPush({ subscription: localSubscription });
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/push" && init?.method === "DELETE")
        return Promise.resolve(new Response(null, { status: 204 }));
      if (path === "/api/push")
        return json({ available: true, public_key: "AQID", enrolled: true });
      if (path === "/api/push/reconcile")
        return json({ enrolled: true, remove_local: false });
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderPush();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Disable push notifications",
    }),
  );

  await waitFor(() => expect(unsubscribe).toHaveBeenCalledOnce());
  expect(
    screen.queryByText("Push notifications are enabled on this device."),
  ).not.toBeInTheDocument();
  expect(
    requests.find(
      ({ path, init }) => path === "/api/push" && init?.method === "DELETE",
    )?.init,
  ).toMatchObject({
    method: "DELETE",
    headers: { "X-Memento-CSRF": trustedSession.csrf_token },
  });
});

test("public sessions show unavailable without contacting push APIs", () => {
  const requestPermission = vi.fn();
  vi.stubGlobal("Notification", { permission: "default", requestPermission });
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    void input;
    return json({ available: true, public_key: "AQID", enrolled: false });
  });
  vi.stubGlobal("fetch", fetchMock);

  renderPush({ ...trustedSession, session_type: "public" });

  expect(
    screen.getByText("Push notifications are unavailable on public computers."),
  ).toBeVisible();
  expect(fetchMock).not.toHaveBeenCalled();
  expect(requestPermission).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("button", { name: "Enable push notifications" }),
  ).not.toBeInTheDocument();
});

test.each([
  ["iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"],
  ["iPad", "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X)"],
])(
  "%s outside standalone mode shows installation guidance",
  async (_device, userAgent) => {
    const browser = installBrowserPush({ userAgent, standalone: false });
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        json({ available: true, public_key: "AQID", enrolled: false }),
      ),
    );

    renderPush();

    expect(
      await screen.findByText(/Add Memento to your iPhone or iPad Home Screen/),
    ).toBeVisible();
    expect(browser.getSubscription).not.toHaveBeenCalled();
    expect(browser.subscribe).not.toHaveBeenCalled();
    expect(browser.requestPermission).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("button", { name: "Enable push notifications" }),
    ).not.toBeInTheDocument();
  },
);

test("Android relies on capability detection without an installation gate", async () => {
  installBrowserPush({
    userAgent: "Mozilla/5.0 (Linux; Android 15) Chrome/130 Mobile",
    standalone: false,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) =>
      requestPath(input) === "/api/push/reconcile"
        ? json({ enrolled: false, remove_local: false })
        : json({ available: true, public_key: "AQID", enrolled: false }),
    ),
  );

  renderPush();

  expect(
    await screen.findByRole("button", { name: "Enable push notifications" }),
  ).toBeVisible();
  expect(screen.queryByText(/Home Screen/)).not.toBeInTheDocument();
});

test("denied permission shows settings guidance without prompting again", async () => {
  const browser = installBrowserPush({ permission: "denied" });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) =>
      requestPath(input) === "/api/push/reconcile"
        ? json({ enrolled: false, remove_local: false })
        : json({ available: true, public_key: "AQID", enrolled: false }),
    ),
  );

  renderPush();

  expect(await screen.findByText(/Push permission is blocked/)).toBeVisible();
  expect(browser.requestPermission).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("button", { name: "Enable push notifications" }),
  ).not.toBeInTheDocument();
});
