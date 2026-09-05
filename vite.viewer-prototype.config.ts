import { fileURLToPath } from "node:url";
import { defineConfig, mergeConfig } from "vite";

import config from "./vite.config";

// Only the throwaway dev server serves the bundled, generated sample media.
export default mergeConfig(
  config,
  defineConfig({
    publicDir: fileURLToPath(
      new URL(
        "./app/components/pages/viewer-prototype/public",
        import.meta.url,
      ),
    ),
    server: { host: "127.0.0.1" },
  }),
);
