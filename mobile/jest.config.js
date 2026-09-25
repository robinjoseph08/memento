const preset = require("jest-expo/jest-preset");

/** @type {import("jest").Config} */
module.exports = {
  preset: "jest-expo",
  setupFiles: ["./jest.setup.js"],
  moduleNameMapper: { "^@/(.*)$": "<rootDir>/src/$1" },
  // The icon package ships ES modules as .mjs files, so Babel must transform
  // it the way it does the Expo packages the preset already lists.
  transform: {
    ...preset.transform,
    "\\.mjs$": preset.transform["\\.[jt]sx?$"],
  },
  transformIgnorePatterns: preset.transformIgnorePatterns.map((pattern) =>
    pattern.replace(
      "standard-navigation",
      "standard-navigation|lucide-react-native",
    ),
  ),
};
