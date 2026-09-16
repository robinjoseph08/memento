// A blurred, faded copy of an Album cover behind a header, so the page takes
// on that Album's own colors. The parent needs `relative isolate`; the wash
// spreads a little past its edges and fades out toward them.
export function CoverWash({ src }: { src: string }) {
  if (!src) return null;
  return (
    <img
      alt=""
      aria-hidden="true"
      className="pointer-events-none absolute -inset-x-10 -inset-y-12 -z-10 h-[calc(100%+6rem)] w-[calc(100%+5rem)] rounded-3xl [mask-image:radial-gradient(closest-side,black,transparent)] object-cover opacity-30 blur-3xl saturate-150"
      decoding="async"
      src={src}
    />
  );
}
