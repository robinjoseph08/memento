import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

const apiProxyTarget =
  process.env.MEMENTO_API_PROXY_TARGET ?? "http://127.0.0.1:8081";

function outputFiles(directory: string, prefix = ""): string[] {
  return readdirSync(directory, { withFileTypes: true })
    .sort((left, right) => left.name.localeCompare(right.name))
    .flatMap((entry) => {
      const relativePath = prefix ? `${prefix}/${entry.name}` : entry.name;
      return entry.isDirectory()
        ? outputFiles(resolve(directory, entry.name), relativePath)
        : [relativePath];
    });
}

const serviceWorkerRevision = (): Plugin => {
  const revisionPlaceholder = "__MEMENTO_BUILD_REVISION__";

  return {
    name: "memento-service-worker-revision",
    apply: "build",
    writeBundle(options) {
      if (!options.dir) {
        throw new Error("service worker revision requires a build directory");
      }

      const workerPath = resolve(options.dir, "service-worker.js");
      const worker = readFileSync(workerPath, "utf8");
      const graph = createHash("sha256");
      for (const fileName of outputFiles(options.dir)) {
        graph.update(fileName);
        graph.update("\0");
        graph.update(
          fileName === "service-worker.js"
            ? worker
            : readFileSync(resolve(options.dir, fileName)),
        );
        graph.update("\0");
      }
      const revision = graph.digest("hex").slice(0, 20);
      const occurrences = worker.split(revisionPlaceholder).length - 1;
      if (occurrences !== 1) {
        throw new Error(
          `expected one service worker revision placeholder, found ${occurrences}`,
        );
      }
      writeFileSync(workerPath, worker.replace(revisionPlaceholder, revision));
    },
  };
};

export default defineConfig({
  clearScreen: false,
  plugins: [react(), tailwindcss(), serviceWorkerRevision()],
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
