/** @type {import("jest").Config} */
module.exports = {
  preset: "jest-expo",
  setupFiles: ["./jest.setup.js"],
  moduleNameMapper: { "^@/(.*)$": "<rootDir>/src/$1" },
};
