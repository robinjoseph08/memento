import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";

import "./styles.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("Root element not found");
}

const ViewerPrototype = import.meta.env.DEV
  ? lazy(() => import("./components/pages/viewer-prototype/ViewerPrototype"))
  : null;

const CuratorPrototype = import.meta.env.DEV
  ? lazy(() => import("./components/pages/curator-prototype/CuratorPrototype"))
  : null;

createRoot(root).render(
  <StrictMode>
    {ViewerPrototype &&
    window.location.pathname.startsWith("/prototype/viewer") ? (
      <Suspense fallback={<p>Opening album…</p>}>
        <ViewerPrototype />
      </Suspense>
    ) : CuratorPrototype &&
      window.location.pathname.startsWith("/prototype/curator") ? (
      <Suspense fallback={<p>Opening Album editor...</p>}>
        <CuratorPrototype />
      </Suspense>
    ) : (
      <App />
    )}
  </StrictMode>,
);
