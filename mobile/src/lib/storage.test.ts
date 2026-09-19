import AsyncStorage from "@react-native-async-storage/async-storage";

import {
  forgetInstallation,
  rememberInstallation,
  rememberedInstallation,
} from "./storage";

const home = "https://photos.example.com";
const other = "https://other.example.com";

// Keys carry the origin percent-encoded.
function stored(keys: readonly string[], origin: string) {
  return keys.filter((key) => key.includes(`:${encodeURIComponent(origin)}:`));
}

beforeEach(() => AsyncStorage.clear());

it("remembers nothing at first", async () => {
  await expect(rememberedInstallation()).resolves.toBeNull();
});

it("remembers the connected Installation across restarts", async () => {
  await rememberInstallation(home);
  await expect(rememberedInstallation()).resolves.toEqual(home);
});

it("stores each Installation under its own origin", async () => {
  await rememberInstallation(home);
  await rememberInstallation(other);
  await expect(rememberedInstallation()).resolves.toEqual(other);

  const keys = await AsyncStorage.getAllKeys();
  expect(stored(keys, home)).toHaveLength(1);
  expect(stored(keys, other)).toHaveLength(1);
});

it("forgets one Installation without touching another", async () => {
  await rememberInstallation(home);
  await rememberInstallation(other);
  await forgetInstallation(other);

  await expect(rememberedInstallation()).resolves.toBeNull();
  const keys = await AsyncStorage.getAllKeys();
  expect(stored(keys, home)).toHaveLength(1);
  expect(stored(keys, other)).toHaveLength(0);
});

it("keeps an Installation on another port of the same host", async () => {
  const onPort = `${home}:8443`;
  await rememberInstallation(onPort);
  await rememberInstallation(home);
  await forgetInstallation(home);

  expect(stored(await AsyncStorage.getAllKeys(), onPort)).toHaveLength(1);
});

it("starts over when what it stored cannot be read", async () => {
  await rememberInstallation(home);
  const [key] = stored(await AsyncStorage.getAllKeys(), home);
  await AsyncStorage.setItem(key, "{not json");

  await expect(rememberedInstallation()).resolves.toBeNull();
});
