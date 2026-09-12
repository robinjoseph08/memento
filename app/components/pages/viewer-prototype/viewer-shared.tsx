// PROTOTYPE. Pieces every viewer variant composes: data hooks for the member
// endpoints, the routed lightbox, the video player with chapters, and small
// formatting helpers.
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useQuery } from "@tanstack/react-query";
import {
  ChevronLeft,
  ChevronRight,
  Download,
  ListVideo,
  Play,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { usePrivateScope } from "../../../hooks/queries/people";
import { request } from "../../../lib/http";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../../ui/popover";
import type {
  MemberAlbum,
  ViewerAlbum,
  ViewerEntry,
} from "../curator-prototype/model";

export type { MemberAlbum, ViewerAlbum, ViewerEntry };

export function useMemberAlbums() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "member-albums"],
    queryFn: ({ signal }) => request<MemberAlbum[]>("/api/albums", { signal }),
    retry: false,
  });
}

export function useMemberAlbum(id: string) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "member-album", id],
    queryFn: ({ signal }) =>
      request<ViewerAlbum>(`/api/albums/${encodeURIComponent(id)}`, {
        signal,
      }),
    retry: false,
    enabled: !!id,
  });
}

export function countLabel(count: number, singular: string, plural: string) {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function dayLabel(day: string, weekday = true) {
  const date = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
    ...(weekday ? { weekday: "long" } : {}),
  });
}

export function dateRange(start: string, end: string) {
  if (!start) return "";
  const same = start === end;
  const sameYear = start.slice(0, 4) === end.slice(0, 4);
  const first = new Date(`${start}T00:00:00Z`).toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    ...(same || !sameYear ? { year: "numeric" } : {}),
  });
  if (same) return first;
  return `${first} to ${dayLabel(end, false)}`;
}

export function ratio(entry: ViewerEntry) {
  return entry.width && entry.height ? entry.width / entry.height : 1.5;
}

export function entriesOfKind(album: ViewerAlbum, kind: "IMAGE" | "VIDEO") {
  return album.days
    .map((day) => ({
      ...day,
      entries: day.entries.filter((entry) => entry.kind === kind),
    }))
    .filter((day) => day.entries.length > 0);
}

// Rows that preserve every aspect ratio: items join a row until its combined
// width-to-height ratio exceeds the target, then the row is laid out with
// each item's ratio as its flex share.
export function justifiedRows(entries: ViewerEntry[], targetRatio: number) {
  const rows: ViewerEntry[][] = [];
  for (const entry of entries) {
    const last = rows.at(-1);
    const sum = last?.reduce((total, item) => total + ratio(item), 0) ?? 0;
    if (!last || sum + ratio(entry) > targetRatio + 0.01) rows.push([entry]);
    else last.push(entry);
  }
  return rows;
}

export function JustifiedRow({
  row,
  targetRatio,
  last,
  gap = "gap-1",
  children,
}: {
  row: ViewerEntry[];
  targetRatio: number;
  last: boolean;
  gap?: string;
  children: (entry: ViewerEntry) => React.ReactNode;
}) {
  const total = row.reduce((sum, entry) => sum + ratio(entry), 0);
  // Short final rows keep their natural size instead of stretching.
  const filler = last || total < targetRatio * 0.7 ? targetRatio - total : 0;
  return (
    <div className={cn("flex", gap)}>
      {row.map((entry) => (
        <div
          className="min-w-0"
          key={entry.id}
          style={{ flex: `${ratio(entry)} 1 0%` }}
        >
          {children(entry)}
        </div>
      ))}
      {filler > 0.01 && <div style={{ flex: `${filler} 1 0%` }} />}
    </div>
  );
}

export function useTargetRatio(desktop = 4.5, tablet = 3, phone = 1.5) {
  const [target, setTarget] = useState(() => pick());
  function pick() {
    if (typeof window === "undefined") return desktop;
    return window.innerWidth < 600
      ? phone
      : window.innerWidth < 1000
        ? tablet
        : desktop;
  }
  useEffect(() => {
    const resize = () => setTarget(pick());
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [desktop, tablet, phone]);
  return target;
}

export function PlayBadge({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "pointer-events-none absolute flex size-10 items-center justify-center rounded-full bg-black/60 text-white",
        className,
      )}
    >
      <Play className="ml-0.5 size-5" fill="currentColor" strokeWidth={0} />
    </span>
  );
}

// ---------------------------------------------------------------------------
// Video player with a temporary chapter overlay.

export function VideoPlayer({ entry }: { entry: ViewerEntry }) {
  const playerRef = useRef<HTMLVideoElement>(null);
  const [position, setPosition] = useState(0);
  const [open, setOpen] = useState(false);
  const active = entry.chapters.reduce(
    (current, chapter, index) => (chapter.time <= position ? index : current),
    0,
  );
  return (
    <div className="relative max-h-full max-w-full">
      <video
        aria-label={entry.title}
        autoPlay
        className="max-h-[calc(100dvh-200px)] max-w-full rounded-sm bg-black"
        controls
        onTimeUpdate={(event) => setPosition(event.currentTarget.currentTime)}
        playsInline
        poster={entry.thumbnail_url}
        preload="metadata"
        ref={playerRef}
      >
        <source src={entry.src} type="video/mp4" />
      </video>
      <Popover onOpenChange={setOpen} open={open}>
        <PopoverTrigger asChild>
          <Button
            className="absolute top-3 right-3 gap-2 bg-black/60 text-white hover:bg-black/75"
            size="sm"
          >
            <ListVideo
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            Chapters
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-72 p-2">
          <p className="px-2 pt-1 pb-2 font-heading text-lg">Chapters</p>
          {entry.chapters.length === 0 ? (
            <p className="px-2 pb-2 text-xs text-muted">
              No chapters found. Use the playback bar to skip ahead.
            </p>
          ) : (
            <ol>
              {entry.chapters.map((chapter, index) => (
                <li key={chapter.time}>
                  <button
                    aria-current={active === index ? "true" : undefined}
                    className={cn(
                      "flex w-full cursor-pointer items-center gap-3 rounded-md p-2 text-left text-sm hover:bg-surface",
                      active === index && "bg-surface",
                    )}
                    onClick={() => {
                      if (playerRef.current)
                        playerRef.current.currentTime = chapter.time;
                      setPosition(chapter.time);
                      setOpen(false);
                    }}
                    type="button"
                  >
                    <span className="relative h-9 w-16 shrink-0 overflow-hidden rounded-sm">
                      <img
                        alt=""
                        className="size-full object-cover"
                        src={entry.thumbnail_url}
                      />
                      {active === index && (
                        <PlayBadge className="inset-0 m-auto size-6" />
                      )}
                    </span>
                    <span className="min-w-0 flex-1 truncate">
                      {chapter.title}
                    </span>
                    {active === index && (
                      <span className="text-xs text-accent-foreground">
                        Now
                      </span>
                    )}
                  </button>
                </li>
              ))}
            </ol>
          )}
        </PopoverContent>
      </Popover>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Routed full-screen lightbox shared by every variant.

export function Lightbox({
  items,
  current,
  closeTo,
  linkTo,
  title,
  filmstrip = true,
}: {
  items: ViewerEntry[];
  current: string;
  closeTo: string;
  linkTo: (entry: ViewerEntry) => string;
  title: string;
  filmstrip?: boolean;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const index = items.findIndex((item) => item.id === current);
  const entry = items[index];
  const touchRef = useRef<{ x: number; y: number } | null>(null);
  const stripRef = useRef<HTMLElement>(null);
  const kind = entry?.kind === "VIDEO" ? "video" : "photo";
  const close = () =>
    location.state?.fromGrid
      ? navigate(-1)
      : navigate(closeTo, { replace: true });
  const go = (next: ViewerEntry) =>
    navigate(linkTo(next), { replace: true, state: location.state });
  const step = (delta: number) => {
    const next = items[index + delta];
    if (next) go(next);
  };
  useEffect(() => {
    stripRef.current
      ?.querySelector('[aria-current="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "center" });
  }, [current]);
  useEffect(() => {
    if (!entry) navigate(closeTo, { replace: true });
  }, [entry, closeTo, navigate]);
  if (!entry) return null;
  return (
    <DialogPrimitive.Root onOpenChange={(open) => !open && close()} open>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-background" />
        <DialogPrimitive.Content
          aria-label={`${kind === "video" ? "Video" : "Photo"} ${index + 1} of ${items.length}`}
          className="fixed inset-0 z-50 flex flex-col text-foreground outline-none"
          onKeyDown={(event) => {
            if (
              event.target instanceof Element &&
              event.target.closest("video, [data-slot=popover-content]")
            )
              return;
            if (event.key === "ArrowLeft") {
              event.preventDefault();
              step(-1);
            }
            if (event.key === "ArrowRight") {
              event.preventDefault();
              step(1);
            }
          }}
          // Focus the stage itself so arrow keys work at once and the close
          // button does not light up on open. Radix restores focus on close.
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            (event.currentTarget as HTMLElement).focus();
          }}
        >
          <DialogPrimitive.Title className="sr-only">
            {entry.kind === "VIDEO" ? entry.title : title}
          </DialogPrimitive.Title>
          <DialogPrimitive.Description className="sr-only">
            {index + 1} of {items.length}
          </DialogPrimitive.Description>
          <header className="flex items-start gap-3 px-3 pt-3 min-[761px]:px-5">
            <DialogPrimitive.Close asChild>
              <Button
                aria-label={`Close ${kind}`}
                className="size-11 rounded-full p-0"
                variant="ghost"
              >
                <X aria-hidden="true" className="size-5" strokeWidth={1.5} />
              </Button>
            </DialogPrimitive.Close>
            <div className="min-w-0 flex-1 pt-1.5">
              <p className="truncate font-heading text-lg leading-tight">
                {entry.kind === "VIDEO" ? entry.title : title}
              </p>
              <p className="text-xs text-muted">
                {index + 1} of {items.length}
              </p>
            </div>
            <Button
              aria-label={`Download ${kind}`}
              asChild
              className="size-11 rounded-full p-0"
              variant="ghost"
            >
              <a download={entry.filename} href={entry.download_url}>
                <Download
                  aria-hidden="true"
                  className="size-5"
                  strokeWidth={1.5}
                />
              </a>
            </Button>
          </header>
          <div
            className="relative flex min-h-0 flex-1 items-center justify-center px-14 py-3"
            onTouchEnd={(event) => {
              if (!touchRef.current) return;
              const dx = touchRef.current.x - event.changedTouches[0].clientX;
              const dy = touchRef.current.y - event.changedTouches[0].clientY;
              if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy))
                step(dx > 0 ? 1 : -1);
              touchRef.current = null;
            }}
            onTouchStart={(event) => {
              if (entry.kind === "VIDEO") return;
              touchRef.current = {
                x: event.touches[0].clientX,
                y: event.touches[0].clientY,
              };
            }}
          >
            <Button
              aria-label={`Previous ${kind}`}
              className="absolute top-1/2 left-2 size-11 -translate-y-1/2 rounded-full p-0"
              disabled={index === 0}
              onClick={() => step(-1)}
              variant="ghost"
            >
              <ChevronLeft
                aria-hidden="true"
                className="size-7"
                strokeWidth={1.5}
              />
            </Button>
            {entry.kind === "VIDEO" ? (
              <VideoPlayer entry={entry} key={entry.id} />
            ) : (
              <img
                alt={entry.filename}
                className="max-h-full max-w-full rounded-sm object-contain"
                draggable={false}
                src={entry.full_url}
              />
            )}
            <Button
              aria-label={`Next ${kind}`}
              className="absolute top-1/2 right-2 size-11 -translate-y-1/2 rounded-full p-0"
              disabled={index === items.length - 1}
              onClick={() => step(1)}
              variant="ghost"
            >
              <ChevronRight
                aria-hidden="true"
                className="size-7"
                strokeWidth={1.5}
              />
            </Button>
          </div>
          <footer className="pb-3 text-center text-xs text-muted">
            {dayLabel(entry.captured_at.slice(0, 10))}
          </footer>
          {filmstrip && (
            <nav
              aria-label={
                kind === "video" ? "Videos in album" : "Photos in album"
              }
              className="flex justify-center-safe gap-0.5 overflow-x-auto px-3 pb-4"
              ref={stripRef}
            >
              {items.map((item, i) => (
                <button
                  aria-current={item.id === current ? "true" : undefined}
                  aria-label={`Go to ${kind} ${i + 1}`}
                  className={cn(
                    "relative h-12 shrink-0 cursor-pointer overflow-hidden rounded-sm opacity-60 outline-2 outline-offset-0 outline-transparent hover:opacity-100 focus-visible:outline-ring aria-[current=true]:opacity-100 aria-[current=true]:outline-primary",
                  )}
                  key={item.id}
                  onClick={() => go(item)}
                  type="button"
                >
                  <img
                    alt=""
                    className="h-full w-auto"
                    src={item.thumbnail_url}
                  />
                  {item.kind === "VIDEO" && (
                    <PlayBadge className="inset-0 m-auto size-5" />
                  )}
                </button>
              ))}
            </nav>
          )}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

export function EmptyKind({
  kind,
  otherLink,
}: {
  kind: "IMAGE" | "VIDEO";
  otherLink: string;
}) {
  const video = kind === "VIDEO";
  return (
    <section className="py-14 text-center">
      <h2 className="font-heading text-[27px]/[1.2]">
        No {video ? "videos" : "photos"} in this album
      </h2>
      <p className="mx-auto mt-3 max-w-100 text-sm text-muted">
        {video
          ? "You can see this album's photos in the Photos tab."
          : "You can watch this album's videos in the Videos tab."}
      </p>
      <Button asChild className="mt-6" variant="outline">
        <a href={otherLink}>View {video ? "photos" : "videos"}</a>
      </Button>
    </section>
  );
}
