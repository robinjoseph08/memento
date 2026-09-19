// Storage is not a test seam. Tests run against the library's in-memory
// implementation and only ever replace the HTTP adapter.
jest.mock("@react-native-async-storage/async-storage", () =>
  require("@react-native-async-storage/async-storage/jest/async-storage-mock"),
);

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
