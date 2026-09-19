import AsyncStorage from "@react-native-async-storage/async-storage";

// Everything the app keeps belongs to one Installation and lives under a key
// that starts with its origin, so a later version can hold several. The only
// exception is CURRENT, which names the origin the app is connected to.
const CURRENT = "memento:current";

// The origin is encoded so that its own colons cannot run into the next part
// of the key. Without that, one host's keys would be a prefix of the keys for
// the same host on another port.
function key(origin: string, name: string) {
  return `memento:installation:${encodeURIComponent(origin)}:${name}`;
}

// rememberedInstallation is the origin the app was connected to when it last
// ran. Anything unreadable counts as nothing remembered.
export async function rememberedInstallation(): Promise<string | null> {
  const origin = await AsyncStorage.getItem(CURRENT);
  const stored =
    origin && (await AsyncStorage.getItem(key(origin, "installation")));
  if (!stored) {
    return null;
  }
  try {
    const installation: unknown = JSON.parse(stored);
    return typeof installation === "object" &&
      installation !== null &&
      "origin" in installation
      ? String(installation.origin)
      : null;
  } catch {
    return null;
  }
}

export async function rememberInstallation(origin: string) {
  await AsyncStorage.setItem(
    key(origin, "installation"),
    JSON.stringify({ origin }),
  );
  await AsyncStorage.setItem(CURRENT, origin);
}

// forgetInstallation removes everything stored under one origin.
export async function forgetInstallation(origin: string) {
  const keys = await AsyncStorage.getAllKeys();
  await AsyncStorage.multiRemove(
    keys.filter((stored) => stored.startsWith(key(origin, ""))),
  );
  if ((await AsyncStorage.getItem(CURRENT)) === origin) {
    await AsyncStorage.removeItem(CURRENT);
  }
}
