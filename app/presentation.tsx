export function MementoMark() {
  return (
    <svg
      aria-label="Memento"
      className="mark"
      role="img"
      viewBox="180 180 664 664"
    >
      <rect
        fill="#0284c7"
        height="440"
        rx="72"
        transform="rotate(-14 437 488)"
        width="340"
        x="267"
        y="268"
      />
      <rect
        fill="#38bdf8"
        height="440"
        rx="72"
        transform="rotate(14 587 488)"
        width="340"
        x="417"
        y="268"
      />
      <rect fill="#bae6fd" height="390" rx="72" width="410" x="307" y="354" />
    </svg>
  );
}

export function BrandHeader() {
  return (
    <>
      <MementoMark />
      <p className="eyebrow">PRIVATE FAMILY ARCHIVE</p>
      <h1 id="memento-title">Memento</h1>
    </>
  );
}

export function ErrorMessage({ error }: { error: Error | null }) {
  if (!error) return null;
  return (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  );
}
