const preset = require("jest-expo/jest-preset");

/** @type {import("jest").Config} */
module.exports = {
  preset: "jest-expo",
  setupFiles: ["./jest.setup.js"],
  // The first test in each screen file pays for transforming React Native and
  // Expo and for the first render, which takes longer than Jest's default five
  // seconds on CI runners.
  testTimeout: 15_000,
  moduleNameMapper: { "^@/(.*)$": "<rootDir>/src/$1" },
  // The icon package ships ES modules as .mjs files, so Babel must transform
  // it the way it does the Expo packages the preset already lists.
  transform: {
    ...preset.transform,
    "\\.mjs$": preset.transform["\\.[jt]sx?$"],
  },
  transformIgnorePatterns: [
    "/node_modules/(?!(.pnpm|react-native|@react-native|@react-native-community|expo|@expo|@expo-google-fonts|react-navigation|@react-navigation|lucide-react-native))",
  ],
};
