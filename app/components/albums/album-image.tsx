import { useState } from "react";

type AlbumImageProps = { src: string; alt: string; fallback: string };

export function AlbumImage(props: AlbumImageProps) {
  return <Image key={props.src} {...props} />;
}

function Image({ src, alt, fallback }: AlbumImageProps) {
  const [failed, setFailed] = useState(false);
  if (!src || failed)
    return (
      <div className="flex aspect-[4/3] items-center justify-center rounded-sm bg-surface px-3 text-center text-sm text-muted">
        {fallback}
      </div>
    );
  return (
    <img
      alt={alt}
      className="aspect-[4/3] w-full rounded-sm bg-surface object-cover"
      loading="lazy"
      onError={() => setFailed(true)}
      src={src}
    />
  );
}
