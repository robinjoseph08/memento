import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

const apiProxyTarget =
  process.env.MEMENTO_API_PROXY_TARGET ?? "http://127.0.0.1:8081";

const browserRevisionWorker: Plugin = {
  name: "memento-browser-revision-worker",
  configurePreviewServer(server) {
    if (process.env.MEMENTO_PWA_REVISION_TEST !== "1") return;
    server.middlewares.use("/service-worker.js", (request, response, next) => {
      if (
        !request.headers.cookie
          ?.split(";")
          .some((cookie) => cookie.trim() === "memento-worker-revision=second")
      ) {
        next();
        return;
      }
      const worker = readFileSync(
        resolve(
          server.config.root,
          server.config.build.outDir,
          "service-worker.js",
        ),
        "utf8",
      ).replace(
        "const CACHE_NAME = `${CACHE_PREFIX}v7`;",
        "const CACHE_NAME = `${CACHE_PREFIX}v7-browser-revision`;",
      );
      response.setHeader("Cache-Control", "no-cache");
      response.setHeader("Content-Type", "text/javascript");
      response.end(worker);
    });
  },
};

export default defineConfig({
  clearScreen: false,
  plugins: [react(), tailwindcss(), browserRevisionWorker],
  root: "app",
  publicDir: "../public",
  build: {
    outDir: "../dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "^/api(?:/|$)": apiProxyTarget,
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./test/setup.ts",
  },
});
