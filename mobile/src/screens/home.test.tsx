import AsyncStorage from "@react-native-async-storage/async-storage";
import { act, render, screen, userEvent } from "@testing-library/react-native";

import { AppProviders } from "@/providers";
import { fakeHTTP, notFound } from "@/testing/fake-http";

import { Home } from "./home";

const origin = "https://photos.example.com";
const status = { claimed: true, auth_mode: "google", version: "v1.4.0" };

async function open(http: ReturnType<typeof fakeHTTP>, devAddress = "") {
  await render(
    <AppProviders createHTTP={http.create}>
      <Home devAddress={devAddress} />
    </AppProviders>,
  );
  return userEvent.setup();
}

async function connect(
  user: ReturnType<typeof userEvent.setup>,
  address: string,
) {
  await user.type(await screen.findByLabelText("Memento address"), address);
  await user.press(screen.getByRole("button", { name: "Connect" }));
}

beforeEach(() => AsyncStorage.clear());

it("connects to an Installation and shows its address and version", async () => {
  const user = await open(
    fakeHTTP({ [origin]: { "/api/identity/status": status } }),
  );
  await connect(user, "photos.example.com");

  expect(await screen.findByText("Connected to Memento")).toBeOnTheScreen();
  expect(screen.getByText("photos.example.com")).toBeOnTheScreen();
  expect(screen.getByText("Version 1.4.0")).toBeOnTheScreen();
});

it("starts with the development Installation filled in", async () => {
  await open(fakeHTTP({}), "http://192.168.1.20:5173");
  expect(await screen.findByLabelText("Memento address")).toHaveProp(
    "value",
    "http://192.168.1.20:5173",
  );
});

it.each([
  ["nothing is there", {}],
  [
    "something else is there",
    { [origin]: { "/api/identity/status": notFound } },
  ],
])(
  "says it couldn't find Memento when %s, and keeps the address",
  async (_name, installations) => {
    const user = await open(fakeHTTP(installations));
    await connect(user, "photos.example.com");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "We couldn't find Memento at that address.",
    );
    expect(screen.getByLabelText("Memento address")).toHaveProp(
      "value",
      "photos.example.com",
    );
  },
);

it("explains what to fix before asking the network", async () => {
  const http = fakeHTTP({});
  const user = await open(http);
  await user.press(await screen.findByRole("button", { name: "Connect" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Enter your Memento address.",
  );
  expect(http.requests).toEqual([]);
});

it("asks for an update when the Installation is too old", async () => {
  const user = await open(
    fakeHTTP({
      [origin]: { "/api/identity/status": { ...status, version: "v0.2.0" } },
    }),
  );
  await connect(user, origin);

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Memento needs an update before the app can use it. Ask whoever runs your Memento to update it.",
  );
});

it("remembers the Installation after a restart", async () => {
  const http = fakeHTTP({ [origin]: { "/api/identity/status": status } });
  const user = await open(http);
  await connect(user, origin);
  await screen.findByText("Connected to Memento");
  await screen.unmount();

  await open(http);
  expect(await screen.findByText("Connected to Memento")).toBeOnTheScreen();
  expect(screen.getByText("photos.example.com")).toBeOnTheScreen();
  // The version is not remembered. It comes from asking again.
  expect(await screen.findByText("Version 1.4.0")).toBeOnTheScreen();
});

it("says so when a remembered Installation stops answering", async () => {
  const user = await open(
    fakeHTTP({ [origin]: { "/api/identity/status": status } }),
  );
  await connect(user, origin);
  await screen.findByText("Connected to Memento");
  await screen.unmount();

  await open(fakeHTTP({}));
  expect(
    await screen.findByText("We can't reach your Memento right now."),
  ).toBeOnTheScreen();
  expect(screen.getByText("photos.example.com")).toBeOnTheScreen();
});

it("does not reconnect when a check finishes after leaving for a different address", async () => {
  let answer = () => {};
  const slow = new Promise(
    (resolve) => (answer = () => resolve({ ...status, version: "v1.5.0" })),
  );
  const http = fakeHTTP({ [origin]: { "/api/identity/status": status } });
  const user = await open(http);
  await connect(user, origin);
  await screen.findByText("Connected to Memento");
  await screen.unmount();

  // After a restart the check is still in flight when the Person leaves.
  const user2 = await open(
    fakeHTTP({ [origin]: { "/api/identity/status": () => slow } }),
  );
  await user2.press(
    await screen.findByRole("button", { name: "Use a different address" }),
  );
  await screen.findByLabelText("Memento address");
  await act(async () => answer());

  expect(screen.getByLabelText("Memento address")).toBeOnTheScreen();
  expect(screen.queryByText("Connected to Memento")).not.toBeOnTheScreen();
});

it("goes back to the connect screen to use a different address", async () => {
  const http = fakeHTTP({ [origin]: { "/api/identity/status": status } });
  const user = await open(http);
  await connect(user, origin);
  await user.press(
    await screen.findByRole("button", { name: "Use a different address" }),
  );
  expect(await screen.findByLabelText("Memento address")).toBeOnTheScreen();
  await screen.unmount();

  await open(http);
  expect(await screen.findByLabelText("Memento address")).toBeOnTheScreen();
});
