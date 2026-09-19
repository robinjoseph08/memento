import { hostname } from "node:os";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const apiURL = process.env.VITE_API_URL;
// Other machines on the network reach the dev server by this machine's name,
// which mDNS advertises with a .local suffix.
const machine = hostname().toLowerCase();
// The Mobile App's browser preview is served by Expo on another port, so its
// requests arrive cross-origin. Vite only allows localhost origins by default.
// This also allows pages served from this machine or a private network
// address, and still refuses arbitrary websites.
const privateNetworkOrigin = new RegExp(
  `^http://(localhost|127\\.0\\.0\\.1|${machine.replaceAll(".", "\\.")}(\\.local)?|10(\\.\\d+){3}|192\\.168(\\.\\d+){2}|172\\.(1[6-9]|2\\d|3[01])(\\.\\d+){2})(:\\d+)?$`,
);

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
    cors: { origin: privateNetworkOrigin },
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
