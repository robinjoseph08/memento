export function Wordmark() {
  return (
    <span className="inline-flex items-center gap-1.75 min-[381px]:gap-2.75">
      <svg
        aria-hidden="true"
        className="size-[29px] text-primary min-[381px]:size-9"
        fill="none"
        height="36"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="3"
        viewBox="0 0 32 32"
        width="36"
      >
        <path d="M5 22V8a4 4 0 0 1 4-4h13" />
        <rect height="19" rx="3" width="19" x="11" y="10" />
      </svg>
      <span className="font-heading text-[26px] leading-none font-normal tracking-[-0.7px] min-[381px]:text-[29px]">
        memento
      </span>
    </span>
  );
}
