import AsyncStorage from "@react-native-async-storage/async-storage";
import { render, screen, userEvent } from "@testing-library/react-native";
import * as WebBrowser from "expo-web-browser";

import { HTTPError } from "@/lib/http";
import { AppProviders } from "@/providers";
import { fakeHTTP, unauthenticated } from "@/testing/fake-http";

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
const album = (id: string, title: string, photos: number) => ({
  id,
  title,
  description: "",
  photo_count: photos,
  video_count: 1,
  start_date: "2026-07-04",
  end_date: "2026-07-05",
  cover_url: `/api/media/viewer/alex/entries/${id}/thumbnail?v=1`,
  cover_preview_url: `/api/media/viewer/alex/entries/${id}/preview?v=1`,
  days: [],
});
const summer = album("summer", "Summer", 12);
const winter = album("winter", "Winter", 1);

// The browser sheet is the one native step. Each test says what it comes
// back with; the sheet itself is faked by jest.setup.js.
const sheet = jest.mocked(WebBrowser.openAuthSessionAsync);

function installation(replies: Record<string, unknown> = {}) {
  return fakeHTTP({
    [origin]: {
      "/api/identity/status": status,
      "/api/identity/mobile/exchange": { token: "phone-token", person },
      "/api/identity/sign-out": undefined,
      "/api/albums": [summer, winter],
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
  return screen.findByRole("button", { name: "Sign in" });
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

it("signs in through the browser sheet and lists Albums with their covers", async () => {
  const http = installation();
  const user = await open(http);
  await signIn(user);

  expect(await screen.findByText("Summer")).toBeOnTheScreen();
  expect(screen.getByText("12 photos, 1 video")).toBeOnTheScreen();
  expect(screen.getByText("Winter")).toBeOnTheScreen();
  expect(screen.getByText("1 photo, 1 video")).toBeOnTheScreen();
  // The image component keeps sources as a list of candidates.
  expect(screen.getByLabelText("Summer")).toHaveProp("source", [
    {
      uri: `${origin}/api/media/viewer/alex/entries/summer/thumbnail?v=1`,
      headers: { Authorization: "Bearer phone-token" },
    },
  ]);

  const [url, returnTo] = sheet.mock.calls[0];
  expect(url).toBe(
    `${origin}/sign-in?return_to=${encodeURIComponent(String(returnTo))}`,
  );
  expect(http.requests).toContainEqual({
    url: `${origin}/api/identity/mobile/exchange`,
    token: null,
  });
  expect(http.requests).toContainEqual({
    url: `${origin}/api/albums`,
    token: "phone-token",
  });
});

it("stays signed in after a restart", async () => {
  const http = installation();
  const user = await open(http);
  await signIn(user);
  await screen.findByText("Summer");
  await screen.unmount();

  await open(http);
  expect(await screen.findByText("Summer")).toBeOnTheScreen();
  expect(sheet).toHaveBeenCalledTimes(1);
});

it("stays on sign-in when the Person closes the sheet", async () => {
  const http = installation();
  const user = await open(http);
  await user.press(await connect(user));

  expect(await screen.findByRole("button", { name: "Sign in" })).toBeOnTheScreen();
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

it("says so when the Albums cannot be loaded", async () => {
  const user = await open(
    installation({
      "/api/albums": () => {
        throw new TypeError("Network request failed");
      },
    }),
  );
  await signIn(user);

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "We can't reach your Memento right now.",
  );
  expect(screen.getByRole("button", { name: "Try again" })).toBeOnTheScreen();
});

it("signs out and returns to sign-in with the Installation remembered", async () => {
  const http = installation();
  const user = await open(http);
  await signIn(user);
  await screen.findByText("Summer");
  await user.press(screen.getByRole("button", { name: "Sign out" }));

  expect(await screen.findByRole("button", { name: "Sign in" })).toBeOnTheScreen();
  expect(screen.getByText("photos.example.com")).toBeOnTheScreen();
  expect(http.requests).toContainEqual({
    url: `${origin}/api/identity/sign-out`,
    token: "phone-token",
  });
  await screen.unmount();

  await open(http);
  expect(await screen.findByRole("button", { name: "Sign in" })).toBeOnTheScreen();
});

it("returns to sign-in when the server rejects the session", async () => {
  const user = await open(installation());
  await signIn(user);
  await screen.findByText("Summer");
  await screen.unmount();

  // Deactivated, or signed out everywhere: the token no longer works.
  await open(installation({ "/api/albums": unauthenticated }));
  expect(await screen.findByRole("button", { name: "Sign in" })).toBeOnTheScreen();
  expect(screen.getByText("photos.example.com")).toBeOnTheScreen();
  await screen.unmount();

  // The token is gone for good, not just hidden until the next answer.
  await open(installation());
  expect(await screen.findByRole("button", { name: "Sign in" })).toBeOnTheScreen();
  expect(screen.queryByText("Summer")).not.toBeOnTheScreen();
});
