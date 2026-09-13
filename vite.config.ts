import { hostname } from "node:os";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const apiURL = process.env.VITE_API_URL;
// Other machines on the network reach the dev server by this machine's name,
// which mDNS advertises with a .local suffix.
const machine = hostname().toLowerCase();

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
    allowedHosts: [machine, `${machine}.local`],
    host: true,
    // Keep the browser's Host header so the API can match it against Origin.
    proxy: apiURL
      ? {
          "/api": { target: apiURL },
          "/health": { target: apiURL },
        }
      : undefined,
    strictPort: true,
  },
});
