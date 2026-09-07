import { hostname } from "node:os";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const apiURL = process.env.VITE_API_URL;

export default defineConfig({
  build: {
    emptyOutDir: true,
    outDir: "../internal/webapp/dist",
  },
  clearScreen: false,
  plugins: [react(), tailwindcss()],
  root: "app",
  resolve: { alias: { "@": fileURLToPath(new URL("./app", import.meta.url)) } },
  server: {
    allowedHosts: [hostname()],
    host: true,
    proxy: apiURL
      ? {
          "/api": apiURL,
          "/health": apiURL,
        }
      : undefined,
    strictPort: true,
  },
});
