// Storage is not a test seam. Tests run against the library's in-memory
// implementation and only ever replace the HTTP adapter.
jest.mock("@react-native-async-storage/async-storage", () =>
  require("@react-native-async-storage/async-storage/jest/async-storage-mock"),
);

// Secure storage has no in-memory version of its own, so it is kept in the
// same in-memory store under its own prefix. Clearing one clears both.
jest.mock("expo-secure-store", () => {
  const AsyncStorage = require("@react-native-async-storage/async-storage");
  return {
    getItemAsync: (key) => AsyncStorage.getItem(`secure:${key}`),
    setItemAsync: (key, value) => AsyncStorage.setItem(`secure:${key}`, value),
    deleteItemAsync: (key) => AsyncStorage.removeItem(`secure:${key}`),
  };
});

// The app's own link scheme comes from the Expo manifest, which Jest does
// not load, so the return link is the store app's.
jest.mock("expo-linking", () => ({
  createURL: (path) => `memento://${path}`,
}));

// There is no browser sheet in Jest. A test that signs in says what the
// sheet comes back with; by default the Person closes it.
jest.mock("expo-web-browser", () => ({
  openAuthSessionAsync: jest.fn(async () => ({ type: "cancel" })),
}));

// There is no notch in Jest. The library's own mock reports zero insets.
jest.mock(
  "react-native-safe-area-context",
  () => require("react-native-safe-area-context/jest/mock").default,
);

// TanStack Query keeps unused queries and finished mutations for five minutes.
// Those timers must not keep Jest alive once the tests are done.
const { timeoutManager } = require("@tanstack/react-query");
const unref = (timer) => (timer.unref(), timer);
timeoutManager.setTimeoutProvider({
  setTimeout: (callback, delay) => unref(setTimeout(callback, delay)),
  clearTimeout: (timer) => clearTimeout(timer),
  setInterval: (callback, delay) => unref(setInterval(callback, delay)),
  clearInterval: (timer) => clearInterval(timer),
});
