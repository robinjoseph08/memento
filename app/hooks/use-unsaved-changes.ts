import { useEffect } from "react";
import { useBlocker } from "react-router-dom";

// Guard router navigation and browser exits only while a form has unsaved work.
export function useUnsavedChanges(unsaved: boolean) {
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      unsaved && currentLocation.pathname !== nextLocation.pathname,
  );
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (
      window.confirm("Leave this page? Your sign-in details will not be saved.")
    )
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
