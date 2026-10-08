import { cn } from "../../lib/utils";

// A blurred, faded copy of an Album cover behind a header, so the page takes
// on that Album's own colors. The parent needs `relative`. By default the wash
// spreads a little past the parent's edges, so the parent clips it with
// `isolate overflow-hidden`. A header that lets the wash spread wider passes a
// className to size it and leaves out `isolate`, so the wash also stays
// behind neighboring content.
export function CoverWash({
  className = "-inset-x-10 -inset-y-12 h-[calc(100%+6rem)] w-[calc(100%+5rem)] rounded-3xl",
  src,
}: {
  className?: string;
  src: string;
}) {
  if (!src) return null;
  return (
    <img
      alt=""
      aria-hidden="true"
      className={cn(
        "pointer-events-none absolute -z-10 max-w-none [mask-image:radial-gradient(closest-side,black,transparent)] object-cover opacity-30 blur-3xl saturate-150",
        className,
      )}
      decoding="async"
      src={src}
    />
  );
}
