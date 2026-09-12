import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";

import "./styles.css";

// PROTOTYPE. `pnpm prototype:curator` runs the app against an in-memory API.
if (import.meta.env.DEV && import.meta.env.MODE === "prototype") {
  const { installPrototypeAPI } =
    await import("./components/pages/curator-prototype/stub");
  installPrototypeAPI();
}

const root = document.getElementById("root");

if (!root) {
  throw new Error("Root element not found");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
