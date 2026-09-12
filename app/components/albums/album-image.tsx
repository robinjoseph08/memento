import { useState, type ComponentProps } from "react";

import { cn } from "../../lib/utils";

type AlbumImageProps = {
  src: string;
  alt: string;
  fallback: string;
  className?: string;
  onLoad?: ComponentProps<"img">["onLoad"];
  onError?: ComponentProps<"img">["onError"];
};

export function AlbumImage(props: AlbumImageProps) {
  return <Image key={props.src} {...props} />;
}

function Image({
  src,
  alt,
  fallback,
  className,
  onLoad,
  onError,
}: AlbumImageProps) {
  const [failed, setFailed] = useState(false);
  if (!src || failed)
    return (
      <div
        className={cn(
          "flex aspect-[3/2] items-center justify-center rounded-sm bg-surface px-3 text-center text-xs text-muted",
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
      onError={(event) => {
        setFailed(true);
        onError?.(event);
      }}
      onLoad={onLoad}
      src={src}
    />
  );
}
