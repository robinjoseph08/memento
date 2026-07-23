import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

const apiProxyTarget =
  process.env.MEMENTO_API_PROXY_TARGET ?? "http://127.0.0.1:8081";

export default defineConfig({
  clearScreen: false,
  plugins: [react(), tailwindcss()],
  root: "app",
  publicDir: "../public",
  build: {
    outDir: "../dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": apiProxyTarget,
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./test/setup.ts",
  },
});
