import { useLayoutEffect, useRef } from "react";

// Dialogs opened from a button that then unmounts, or from no button at all,
// have no Radix trigger to return focus to. Remember what was focused before
// the dialog took it; layout effects run before Radix moves focus inside.
export function useReturnFocus(open = true) {
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (open && document.activeElement instanceof HTMLElement)
      returnFocusRef.current = document.activeElement;
  }, [open]);
  return (event: Event) => {
    event.preventDefault();
    const target = returnFocusRef.current;
    if (target?.isConnected) target.focus();
  };
}
