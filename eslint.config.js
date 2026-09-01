import eslintReact from "@eslint-react/eslint-plugin";
import js from "@eslint/js";
import perfectionist from "eslint-plugin-perfectionist";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    ignores: [
      "build/*",
      "app/dist",
      "app/types/generated/*",
      "app/components/ui",
      "internal/webapp/dist",
      "test-results",
      "tmp",
    ],
  },
  {
    files: ["app/**/*.{ts,tsx}"],
    ...eslintReact.configs["recommended-typescript"],
    languageOptions: {
      globals: {
        ...globals.browser,
        __APP_VERSION__: "readonly",
      },
    },
    plugins: {
      ...eslintReact.configs["recommended-typescript"].plugins,
      ...reactHooks.configs.flat["recommended-latest"].plugins,
      perfectionist,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...eslintReact.configs["recommended-typescript"].rules,
      ...reactHooks.configs.flat["recommended-latest"].rules,

      // Prefer the official React Hooks implementations over overlapping
      // @eslint-react rules.
      "@eslint-react/error-boundaries": "off",
      "@eslint-react/exhaustive-deps": "off",
      "@eslint-react/globals": "off",
      "@eslint-react/immutability": "off",
      "@eslint-react/purity": "off",
      "@eslint-react/refs": "off",
      "@eslint-react/rules-of-hooks": "off",
      "@eslint-react/set-state-in-effect": "off",
      "@eslint-react/set-state-in-render": "off",
      "@eslint-react/static-components": "off",
      "@eslint-react/unsupported-syntax": "off",
      "@eslint-react/use-memo": "off",

      "perfectionist/sort-jsx-props": "error",
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
    },
  },
  {
    files: ["*.js"],
    ignores: ["app/**"],
    languageOptions: {
      globals: globals.node,
    },
  },
);
