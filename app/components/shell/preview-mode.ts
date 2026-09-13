import { createContext, use, useLayoutEffect } from "react";

// Curator preview shows another person's view inside the Curator's session.
// While it is on screen the header disables account actions, so the shell
// learns about it from the preview itself rather than from the URL.
export const PreviewModeContext = createContext<{
  active: boolean;
  setActive: (active: boolean) => void;
}>({ active: false, setActive: () => {} });

// Mounting the Curator preview turns the mode on; unmounting turns it off.
export function usePreviewMode() {
  const { setActive } = use(PreviewModeContext);
  useLayoutEffect(() => {
    setActive(true);
    return () => setActive(false);
  }, [setActive]);
}
