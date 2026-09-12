// PROTOTYPE. Floating bar for flipping between variants and resetting the
// fixture. Not part of the design under review, so it is deliberately
// high-contrast and plain. With one variant it only offers the reset.
import { ChevronLeft, ChevronRight, RotateCcw } from "lucide-react";
import { useEffect } from "react";
import { useSearchParams } from "react-router-dom";

import { useAlbumAction } from "./shared";

export function PrototypeSwitcher({
  albumID,
  name,
  variants = [],
  current = "",
}: {
  albumID: string;
  name: string;
  variants?: { key: string; name: string }[];
  current?: string;
}) {
  const [, setParams] = useSearchParams();
  const reset = useAlbumAction(albumID, "reset");
  const index = Math.max(
    0,
    variants.findIndex((variant) => variant.key === current),
  );
  const go = (offset: number) => {
    if (variants.length < 2) return;
    const next = variants[(index + offset + variants.length) % variants.length];
    setParams(
      (previous) => {
        const params = new URLSearchParams();
        params.set("variant", next.key);
        const section = previous.get("section");
        if (section) params.set("section", section);
        const moment = previous.get("moment");
        if (moment) params.set("moment", moment);
        return params;
      },
      { replace: true },
    );
  };
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (
        target?.closest(
          "input, textarea, select, [contenteditable], [role=dialog]",
        )
      )
        return;
      if (event.key === "ArrowLeft") go(-1);
      if (event.key === "ArrowRight") go(1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  if (!import.meta.env.DEV) return null;
  const arrow = (label: string, offset: number, Icon: typeof ChevronLeft) => (
    <button
      aria-label={label}
      className="flex size-8 cursor-pointer items-center justify-center rounded-full hover:bg-background/15"
      onClick={() => go(offset)}
      type="button"
    >
      <Icon aria-hidden="true" className="size-4" />
    </button>
  );
  return (
    <aside
      aria-label="Prototype controls"
      className="fixed right-4 bottom-4 z-40 flex items-center gap-1 rounded-full border border-border bg-foreground py-1 pr-1 pl-2 text-background shadow-lg"
    >
      <span className="px-1 text-[10px] tracking-wide uppercase opacity-70">
        Prototype
      </span>
      {variants.length > 1 ? (
        <>
          {arrow("Previous variant", -1, ChevronLeft)}
          <span className="min-w-28 text-center text-xs font-medium">
            {variants[index].key} ({variants[index].name})
          </span>
          {arrow("Next variant", 1, ChevronRight)}
        </>
      ) : (
        <span className="px-2 text-xs font-medium">{name}</span>
      )}
      <button
        aria-label="Reset fixture"
        className="flex size-8 cursor-pointer items-center justify-center rounded-full border-l border-background/20 hover:bg-background/15"
        disabled={reset.isPending}
        onClick={() => reset.mutate({})}
        title="Reset fixture"
        type="button"
      >
        <RotateCcw aria-hidden="true" className="size-3.5" />
      </button>
    </aside>
  );
}
