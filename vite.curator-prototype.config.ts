import { fileURLToPath } from "node:url";
import { defineConfig, mergeConfig } from "vite";

import config from "./vite.config";

// PROTOTYPE. The throwaway dev server serves the bundled generated sample media
// and never proxies to a backend; the app talks to an in-memory API stub.
export default mergeConfig(
  config,
  defineConfig({
    publicDir: fileURLToPath(
      new URL(
        "./app/components/pages/curator-prototype/public",
        import.meta.url,
      ),
    ),
    server: { host: "127.0.0.1", proxy: undefined },
  }),
);
