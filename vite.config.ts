import { hostname } from "node:os";
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
