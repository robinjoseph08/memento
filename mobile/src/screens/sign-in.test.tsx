import AsyncStorage from "@react-native-async-storage/async-storage";
import { render, screen, userEvent } from "@testing-library/react-native";
import * as WebBrowser from "expo-web-browser";

import { HTTPError } from "@/lib/http";
import { AppProviders } from "@/providers";
import { fakeHTTP } from "@/testing/fake-http";

import { Home } from "./home";

const origin = "https://photos.example.com";
const status = { claimed: true, auth_mode: "google", version: "v1.4.0" };
const person = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  onboarding_completed_at: "2026-02-01T00:00:00Z",
  update_email: "",
  email_updates: false,
  avatar_url: "",
};

// The browser sheet is the one native step. Each test says what it comes
// back with; the sheet itself is faked by jest.setup.js.
const sheet = jest.mocked(WebBrowser.openAuthSessionAsync);

function installation(replies: Record<string, unknown> = {}) {
  return fakeHTTP({
    [origin]: {
      "/api/identity/status": status,
      "/api/identity/mobile/exchange": { token: "phone-token", person },
      "/api/identity/me": person,
      "/api/albums": [],
      ...replies,
    },
  });
}

async function open(http: ReturnType<typeof fakeHTTP>) {
  await render(
    <AppProviders createHTTP={http.create}>
      <Home devAddress="" />
    </AppProviders>,
  );
  return userEvent.setup();
}

async function connect(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText("Memento address"), origin);
  await user.press(screen.getByRole("button", { name: "Connect" }));
  await screen.findByText("Version 1.4.0");
  return screen.getByRole("button", { name: "Sign in" });
}

async function signIn(
  user: ReturnType<typeof userEvent.setup>,
  returned = "memento://sign-in?code=single-use-code",
) {
  sheet.mockResolvedValueOnce({ type: "success", url: returned });
  await user.press(await connect(user));
}

beforeEach(async () => {
  await AsyncStorage.clear();
  sheet.mockClear();
});

it("opens the Installation's web sign-in and exchanges the code it returns", async () => {
  const http = installation();
  const user = await open(http);
  await signIn(user);

  expect(
    await screen.findByRole("heading", { name: "Your albums" }),
  ).toBeOnTheScreen();
  await screen.findByText(/Nothing is shared with you yet/);
  await screen.findByLabelText("Alex");
  const [url, returnTo] = sheet.mock.calls[0];
  expect(url).toBe(
    `${origin}/sign-in?return_to=${encodeURIComponent(String(returnTo))}`,
  );
  expect(http.requests).toContainEqual({
    url: `${origin}/api/identity/mobile/exchange`,
    token: null,
    body: { code: "single-use-code", platform: "iPhone" },
  });
});

it("stays on sign-in when the Person closes the sheet", async () => {
  const http = installation();
  const user = await open(http);
  await user.press(await connect(user));

  expect(
    await screen.findByRole("button", { name: "Sign in" }),
  ).toBeOnTheScreen();
  expect(screen.queryByRole("alert")).not.toBeOnTheScreen();
  expect(
    http.requests.filter((request) => request.url.endsWith("/exchange")),
  ).toEqual([]);
});

it("explains when the code is refused", async () => {
  const user = await open(
    installation({
      "/api/identity/mobile/exchange": () => {
        throw new HTTPError(
          "That sign-in has expired. Start again from the app.",
          401,
        );
      },
    }),
  );
  await signIn(user);

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "That sign-in has expired. Start again from the app.",
  );
  expect(screen.getByRole("button", { name: "Sign in" })).toBeOnTheScreen();
});

it("keeps the sheet's and the network's own errors away from the Person", async () => {
  const user = await open(
    installation({
      "/api/identity/mobile/exchange": () => {
        throw new TypeError("Network request failed");
      },
    }),
  );
  await signIn(user);

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Sign-in could not be completed. Try again.",
  );
});
