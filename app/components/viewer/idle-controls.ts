import { useCallback, useEffect, useRef, useState } from "react";

// How long the lightbox's controls stay up after the last thing that showed
// them.
const IDLE_MS = 3000;

// useIdleControls keeps the lightbox's controls over the photo while someone
// is using them and fades them out once they are just looking. show brings
// them back and restarts the countdown without rendering again when they are
// already up, so it can follow every mouse movement. While held, as when the
// pointer rests on a control, they stay up.
export function useIdleControls(held: boolean) {
  const [visible, setVisible] = useState(true);
  const timerRef = useRef(0);
  const heldRef = useRef(held);

  const arm = useCallback(() => {
    window.clearTimeout(timerRef.current);
    if (!heldRef.current)
      timerRef.current = window.setTimeout(() => setVisible(false), IDLE_MS);
  }, []);
  useEffect(() => {
    heldRef.current = held;
    if (held) window.clearTimeout(timerRef.current);
    else if (visible) arm();
  }, [held, visible, arm]);
  useEffect(() => () => window.clearTimeout(timerRef.current), []);

  const show = useCallback(() => {
    setVisible(true);
    arm();
  }, [arm]);
  const hide = useCallback(() => {
    window.clearTimeout(timerRef.current);
    setVisible(false);
  }, []);
  return { visible, show, hide };
}
