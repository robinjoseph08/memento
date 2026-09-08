import { useState } from "react";

import { cn } from "../../lib/utils";

type AlbumImageProps = {
  src: string;
  alt: string;
  fallback: string;
  className?: string;
};

export function AlbumImage(props: AlbumImageProps) {
  return <Image key={props.src} {...props} />;
}

function Image({ src, alt, fallback, className }: AlbumImageProps) {
  const [failed, setFailed] = useState(false);
  if (!src || failed)
    return (
      <div
        className={cn(
          "flex aspect-[4/3] items-center justify-center rounded-sm bg-surface px-3 text-center text-xs text-muted",
          className,
        )}
      >
        {fallback}
      </div>
    );
  return (
    <img
      alt={alt}
      className={cn(
        "h-auto w-full rounded-sm bg-surface object-contain",
        className,
      )}
      loading="lazy"
      onError={() => setFailed(true)}
      src={src}
    />
  );
}
