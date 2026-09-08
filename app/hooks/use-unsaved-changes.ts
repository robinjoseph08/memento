import { use, useEffect, useLayoutEffect, useRef } from "react";
import { useBlocker } from "react-router-dom";

import { UnsavedChangesContext } from "../lib/forms";

// Guard router navigation and browser exits only while a form has unsaved work.
export function useUnsavedChanges(unsaved: boolean) {
  const sharedRef = use(UnsavedChangesContext);
  const currentRef = useRef(unsaved);
  useLayoutEffect(() => {
    if (!sharedRef) return;
    sharedRef.current = unsaved;
    return () => {
      sharedRef.current = false;
    };
  }, [sharedRef, unsaved]);
  useLayoutEffect(() => {
    currentRef.current = unsaved;
  }, [unsaved]);
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      currentRef.current && currentLocation.pathname !== nextLocation.pathname,
  );
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm("Leave this page? Your changes will not be saved."))
      blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    if (!unsaved) return;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [unsaved]);
}
