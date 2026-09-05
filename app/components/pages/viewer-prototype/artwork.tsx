import type { ComponentProps } from "react";

const paths = {
  back: "m14 6-6 6 6 6",
  next: "m10 6 6 6-6 6",
  close: "m6 6 12 12M18 6 6 18",
  photo: "M4 4h16v16H4zM4 16l5-5 4 4 3-3 4 4M15 8h.01",
  video: "M4 5h16v14H4zM10 9l5 3-5 3z",
  play: "m9 5 11 7-11 7z",
  bell: "M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4",
  download: "M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5",
  chapters: "M9 5h12M9 12h12M9 19h12M3 5h.01M3 12h.01M3 19h.01",
  sun: "M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1 1m12 12 1 1M5 19l1-1M18 6l1-1M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
  moon: "M20 15A9 9 0 0 1 9 3a9 9 0 1 0 11 12",
  settings: "M4 7h16M4 17h16M8 4v6M16 14v6",
  check: "m5 12 4 4L19 6",
  album: "M6 3h14v18H6a3 3 0 0 1 0-6h14M6 3a3 3 0 0 0-3 3v12M8 7h8",
};
export function Icon({
  name,
  ...props
}: ComponentProps<"svg"> & { name: keyof typeof paths }) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      height="20"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.65"
      viewBox="0 0 24 24"
      width="20"
      {...props}
    >
      <path d={paths[name]} />
    </svg>
  );
}

export function Logo() {
  return (
    <svg
      aria-hidden="true"
      className="memento-mark"
      fill="none"
      height="40"
      viewBox="0 0 48 48"
      width="40"
    >
      <>
        <path
          d="M30 7H10a4 4 0 0 0-4 4v20"
          stroke="currentColor"
          strokeLinecap="round"
          strokeWidth="4.5"
        />
        <rect
          height="25"
          rx="5"
          stroke="currentColor"
          strokeWidth="4.5"
          width="27"
          x="15"
          y="16"
        />
      </>
    </svg>
  );
}
