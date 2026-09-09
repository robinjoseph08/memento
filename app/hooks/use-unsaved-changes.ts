import { use, useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { useBlocker } from "react-router-dom";

import { UnsavedChangesContext } from "../lib/forms";

// Register a form with the app-wide navigation guard while it has unsaved work.
export function useUnsavedChanges(unsaved: boolean, includeSearch = false) {
  const sharedRef = use(UnsavedChangesContext);
  const registrationRef = useRef(Symbol("unsaved changes"));
  useLayoutEffect(() => {
    if (!sharedRef) return;
    const registry = sharedRef.current;
    const registration = registrationRef.current;
    if (unsaved) registry.set(registration, includeSearch);
    else registry.delete(registration);
    return () => {
      registry.delete(registration);
    };
  }, [includeSearch, sharedRef, unsaved]);
}

// React Router supports one blocker per router, so the shell owns the blocker
// while individual forms only register their dirty state above.
export function useUnsavedChangesBlocker(
  registryRef: RefObject<Map<symbol, boolean>>,
) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    const registry = registryRef.current;
    if (registry.size === 0) return false;
    if (currentLocation.pathname !== nextLocation.pathname) return true;
    const searchChanged =
      currentLocation.search !== nextLocation.search ||
      currentLocation.hash !== nextLocation.hash;
    return searchChanged && [...registry.values()].some(Boolean);
  });
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm("Leave this page? Your changes will not be saved."))
      blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (registryRef.current.size === 0) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [registryRef]);
}
