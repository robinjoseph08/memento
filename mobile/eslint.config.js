const { defineConfig } = require("eslint/config");
const expo = require("eslint-config-expo/flat");

module.exports = defineConfig([
  expo,
  { ignores: [".expo/*", "dist/*", "src/types/generated/*"] },
  {
    // One HTTP adapter owns every request.
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/lib/http.ts"],
    rules: {
      "no-restricted-globals": [
        "error",
        {
          name: "fetch",
          message: "Go through the HTTP adapter in src/lib/http.ts.",
        },
      ],
    },
  },
]);
