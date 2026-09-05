import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  BrowserRouter,
  Link,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";

import { ViewerPrototypeControls } from "../../ui/viewer-prototype-controls";
import { Icon, Logo } from "./artwork";
import { album, dayLabel, photos, videos, videoTitle } from "./fixtures";

import "./viewer-prototype.css";

// Throwaway viewer prototype, refined to the selected plain cover and Frames mark.
const base = "/prototype/viewer/album";
const albumList = "/prototype/viewer/albums";
type PreviewPhoto = Omit<(typeof photos)[number], "alt"> & { alt: string };
type HeaderProps = {
  cover: string | null;
  coverAlt: string;
  title?: string;
  dateRange?: string;
  photoCount: number;
  videoCount: number;
};

function AlbumFacts({
  photoCount,
  videoCount,
  dateRange,
}: Pick<HeaderProps, "photoCount" | "videoCount" | "dateRange">) {
  return (
    <div className="album-facts">
      <span>
        {dateRange ??
          (photoCount === 0 ? album.videoDateRange : album.dateRange)}
      </span>
      <span className="media-counts">
        <span>
          <Icon name="photo" />
          {photoCount} photos
        </span>
        <span>
          <Icon name="video" />
          {videoCount} videos
        </span>
      </span>
    </div>
  );
}

export function AlbumHeader({
  cover,
  coverAlt,
  photoCount,
  videoCount,
  title = album.title,
  dateRange,
}: HeaderProps) {
  return (
    <header className="album-header open-header">
      <div className="album-heading">
        <h1>{title}</h1>
        <p className="album-description">{album.description}</p>
        <AlbumFacts
          dateRange={dateRange}
          photoCount={photoCount}
          videoCount={videoCount}
        />
      </div>
      {cover ? (
        <img alt={coverAlt} className="album-cover" src={cover} />
      ) : (
        <div
          aria-label="No accessible Moment cover"
          className="album-cover neutral-cover"
        >
          <Icon name="album" />
        </div>
      )}
    </header>
  );
}

export function PhotoGallery({
  search,
  items = photos,
  basePath = base,
}: {
  search: string;
  items?: PreviewPhoto[];
  basePath?: string;
}) {
  const [rowRatio, setRowRatio] = useState(
    window.innerWidth < 600 ? 1.5 : window.innerWidth < 1000 ? 3 : 4.5,
  );
  useEffect(() => {
    const resize = () =>
      setRowRatio(
        window.innerWidth < 600 ? 1.5 : window.innerWidth < 1000 ? 3 : 4.5,
      );
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  return (
    <div className="photo-gallery">
      {[...new Set(items.map((photo) => photo.day))].map((day) => {
        const dayPhotos = items.filter((photo) => photo.day === day);
        const rows = dayPhotos.reduce<PreviewPhoto[][]>((result, photo) => {
          const last = result.at(-1);
          if (
            !last ||
            last.reduce((sum, item) => sum + item.ratio, 0) + photo.ratio >
              rowRatio + 0.01
          )
            result.push([photo]);
          else last.push(photo);
          return result;
        }, []);
        return (
          <section
            aria-labelledby={`day-${day}`}
            className="day-group"
            key={day}
          >
            <div className="day-heading">
              <h2 id={`day-${day}`}>{dayLabel(day)}</h2>
              <span>{dayPhotos.length} photos</span>
            </div>
            <div className="photo-grid">
              {rows.map((row, index) => {
                const total = row.reduce((sum, photo) => sum + photo.ratio, 0);
                const remainder =
                  index === rows.length - 1 || total < rowRatio * 0.7
                    ? Math.max(0, rowRatio - total)
                    : 0;
                return (
                  <div
                    className="photo-row"
                    key={row[0].id}
                    style={{
                      gridTemplateColumns: [
                        ...row.map((photo) => `${photo.ratio}fr`),
                        ...(remainder > 0.01 ? [`${remainder}fr`] : []),
                      ].join(" "),
                    }}
                  >
                    {row.map((photo) => (
                      <Link
                        aria-label={`Open photo: ${photo.alt}`}
                        className="photo-tile"
                        key={photo.id}
                        state={{ fromGrid: true }}
                        to={`${basePath}/photos/${photo.id}${search}`}
                      >
                        <img
                          alt={photo.alt}
                          draggable={false}
                          loading="lazy"
                          src={photo.image}
                          style={{ aspectRatio: photo.ratio }}
                        />
                      </Link>
                    ))}
                  </div>
                );
              })}
            </div>
          </section>
        );
      })}
    </div>
  );
}

function EmptyTab({ tab, search }: { tab: string; search: string }) {
  const isVideo = tab === "videos";
  return (
    <section className="empty-tab">
      <Icon height="38" name={isVideo ? "video" : "photo"} width="38" />
      <h2>No {isVideo ? "videos" : "photos"} in this album</h2>
      <p>
        {isVideo
          ? "You can see this album’s photos in the Photos tab."
          : "You can watch this album’s videos in the Videos tab."}
      </p>
      <Link
        className="action-button"
        to={`${base}/${isVideo ? "photos" : "videos"}${search}`}
      >
        View {isVideo ? "photos" : "videos"}
      </Link>
    </section>
  );
}

export function VideoGallery({
  search,
  items = videos,
  basePath = base,
}: {
  search: string;
  items?: typeof videos;
  basePath?: string;
}) {
  return (
    <div className="video-gallery">
      {[...new Set(items.map((video) => video.day))].map((day) => (
        <section
          aria-labelledby={`video-day-${day}`}
          className="day-group"
          key={day}
        >
          <div className="day-heading">
            <h2 id={`video-day-${day}`}>{dayLabel(day)}</h2>
            <span>
              {items.filter((video) => video.day === day).length} videos
            </span>
          </div>
          <div className="video-grid">
            {items
              .filter((video) => video.day === day)
              .map((video) => (
                <Link
                  aria-label={`Open video: ${videoTitle(video)}`}
                  className="video-card"
                  key={video.id}
                  state={{ fromGrid: true }}
                  to={`${basePath}/videos/${video.id}${search}`}
                >
                  <div className="video-thumbnail">
                    <img alt="" src={video.poster} />
                    <span className="video-play">
                      <Icon name="play" />
                    </span>
                  </div>
                  <h3>{videoTitle(video)}</h3>
                  {video.chapters.length > 0 && (
                    <p>{video.chapters.length} chapters</p>
                  )}
                </Link>
              ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function VideoPlayer({
  video,
  readOnly = false,
}: {
  video: (typeof videos)[number];
  readOnly?: boolean;
}) {
  const playerRef = useRef<HTMLVideoElement>(null);
  const chapterPanelRef = useRef<HTMLDialogElement>(null);
  const [position, setPosition] = useState(0);
  useEffect(() => {
    const closeChapters = () => chapterPanelRef.current?.close();
    window.addEventListener("resize", closeChapters);
    return () => window.removeEventListener("resize", closeChapters);
  }, []);
  const activeChapter = video.chapters.reduce(
    (active, chapter, index) => (chapter.time <= position ? index : active),
    0,
  );
  return (
    <div className="video-player">
      <video
        aria-label={videoTitle(video)}
        autoPlay
        controls
        controlsList={readOnly ? "nodownload" : undefined}
        onTimeUpdate={(event) => setPosition(event.currentTarget.currentTime)}
        playsInline
        poster={video.poster}
        preload="metadata"
        ref={playerRef}
      >
        <source src={video.src} type="video/mp4" />
      </video>
      <button
        className="chapters-trigger"
        onClick={() => {
          const rect = playerRef.current?.getBoundingClientRect();
          const panel = chapterPanelRef.current;
          if (!rect || !panel) return;
          const width = Math.min(350, rect.width - 20, window.innerWidth - 20);
          const top = Math.max(
            10,
            Math.min(rect.top + 10, window.innerHeight - 140),
          );
          const left = Math.max(
            10,
            Math.min(rect.right - width - 10, window.innerWidth - width - 10),
          );
          Object.assign(panel.style, {
            top: `${top}px`,
            left: `${left}px`,
            width: `${width}px`,
            maxHeight: `${Math.min(rect.height - 20, window.innerHeight - top - 10)}px`,
          });
          panel.showModal();
        }}
      >
        <Icon name="chapters" />
        Chapters
      </button>
      <dialog
        aria-labelledby="chapters-title"
        className="chapters-dialog"
        onClick={(event) => {
          if (event.target === event.currentTarget)
            chapterPanelRef.current?.close();
        }}
        ref={chapterPanelRef}
      >
        <div className="flex items-center justify-between">
          <h3 id="chapters-title">Chapters</h3>
          <button
            aria-label="Close chapters"
            className="icon-button"
            onClick={() => chapterPanelRef.current?.close()}
          >
            <Icon name="close" />
          </button>
        </div>
        {video.chapters.length === 0 ? (
          <p className="no-chapters">
            No chapters found. Use the playback bar to skip ahead.
          </p>
        ) : (
          <ol>
            {video.chapters.map((chapter, index) => (
              <li key={chapter.time}>
                <button
                  aria-current={activeChapter === index ? "true" : undefined}
                  onClick={() => {
                    if (playerRef.current)
                      playerRef.current.currentTime = chapter.time;
                    setPosition(chapter.time);
                    chapterPanelRef.current?.close();
                  }}
                >
                  <span className="chapter-thumbnail">
                    <img alt="" src={video.poster} />
                    {activeChapter === index && <Icon name="play" />}
                  </span>
                  <strong>{chapter.title}</strong>
                  {activeChapter === index && (
                    <span className="chapter-playing">Current</span>
                  )}
                </button>
              </li>
            ))}
          </ol>
        )}
      </dialog>
    </div>
  );
}

export function Lightbox({
  mediaId,
  tab,
  search,
  photoItems = photos,
  videoItems = videos,
  basePath = base,
  title = album.title,
  previewName,
}: {
  mediaId: string;
  tab: "photos" | "videos";
  search: string;
  photoItems?: PreviewPhoto[];
  videoItems?: typeof videos;
  basePath?: string;
  title?: string;
  previewName?: string;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const openerRef = useRef(document.activeElement);
  const touchStartRef = useRef<{ x: number; y: number } | null>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const isVideo = tab === "videos";
  const items = isVideo
    ? videoItems.map((video) => ({
        id: video.id,
        image: video.poster,
        day: video.day,
        label: videoTitle(video),
      }))
    : photoItems.map((photo) => ({
        id: photo.id,
        image: photo.image,
        day: photo.day,
        label: photo.alt,
      }));
  const index = items.findIndex((item) => item.id === mediaId);
  const selected = items[index];
  const video = isVideo
    ? videoItems.find((item) => item.id === mediaId)
    : undefined;
  const kind = isVideo ? "video" : "photo";
  const close = () =>
    location.state?.fromGrid
      ? navigate(-1)
      : navigate(`${basePath}/${tab}${search}`, { replace: true });
  const go = (id: string) =>
    navigate(`${basePath}/${tab}/${id}${search}`, {
      replace: true,
      state: location.state,
    });
  const step = (delta: number) => {
    const next = items[index + delta];
    if (next) go(next.id);
  };
  useEffect(() => {
    dialogRef.current?.showModal();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const opener = openerRef.current;
    return () => {
      document.body.style.overflow = overflow;
      if (opener instanceof HTMLElement && opener.isConnected)
        opener.focus({ preventScroll: true });
    };
  }, []);
  useEffect(() => {
    dialogRef.current
      ?.querySelector('.lightbox-filmstrip [aria-current="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "center" });
  }, [mediaId]);
  if (!selected) return <Navigate replace to={`${basePath}/${tab}${search}`} />;
  return (
    <dialog
      aria-label={`${isVideo ? "Video" : "Photo"} ${index + 1} of ${items.length}`}
      className={`lightbox ${isVideo ? "video-lightbox" : ""}`}
      onCancel={(event) => {
        if (event.target !== event.currentTarget) return;
        event.preventDefault();
        close();
      }}
      onKeyDown={(event) => {
        if (
          event.target instanceof Element &&
          event.target.closest("video, .chapters-dialog")
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
      ref={dialogRef}
    >
      <header className="lightbox-toolbar">
        <button
          aria-label={`Close ${kind}`}
          className="icon-button"
          onClick={close}
        >
          <Icon name="close" />
        </button>
        <div>
          <h2>{video ? videoTitle(video) : title}</h2>
          <p>
            {previewName && `Preview as ${previewName}. Read only. `}
            {index + 1} of {items.length}
          </p>
        </div>
        {!previewName && (
          <a
            aria-label={`Download ${kind}`}
            className="icon-button"
            download={video ? video.filename : `memento-${selected.id}.jpg`}
            href={video ? video.src : selected.image}
          >
            <Icon name="download" />
          </a>
        )}
      </header>
      <div
        className="lightbox-stage"
        onTouchEnd={(event) => {
          if (!touchStartRef.current) return;
          const dx = touchStartRef.current.x - event.changedTouches[0].clientX;
          const dy = touchStartRef.current.y - event.changedTouches[0].clientY;
          if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy))
            step(dx > 0 ? 1 : -1);
          touchStartRef.current = null;
        }}
        onTouchStart={(event) => {
          if (isVideo) return;
          touchStartRef.current = {
            x: event.touches[0].clientX,
            y: event.touches[0].clientY,
          };
        }}
      >
        <button
          aria-label={`Previous ${kind}`}
          className="icon-button lightbox-previous"
          disabled={index === 0}
          onClick={() => step(-1)}
        >
          <Icon height="28" name="back" width="28" />
        </button>
        {video ? (
          <VideoPlayer key={video.id} readOnly={!!previewName} video={video} />
        ) : (
          <img alt={selected.label} draggable={false} src={selected.image} />
        )}
        <button
          aria-label={`Next ${kind}`}
          className="icon-button lightbox-next"
          disabled={index === items.length - 1}
          onClick={() => step(1)}
        >
          <Icon height="28" name="next" width="28" />
        </button>
      </div>
      <footer className="lightbox-footer">
        <p>{dayLabel(selected.day)}</p>
      </footer>
      <nav
        aria-label={isVideo ? "Videos in album" : "Photos in album"}
        className="lightbox-filmstrip"
      >
        {items.map((item, i) => (
          <button
            aria-current={item.id === mediaId ? "true" : undefined}
            aria-label={`Go to ${kind} ${i + 1}`}
            key={item.id}
            onClick={() => go(item.id)}
          >
            <img alt="" src={item.image} />
            {isVideo && <Icon name="play" />}
          </button>
        ))}
      </nav>
    </dialog>
  );
}

function ViewerShell({
  search,
  children,
  preview,
}: {
  search: string;
  children: ReactNode;
  preview: HeaderProps;
}) {
  const counts = `${preview.photoCount} photos and ${preview.videoCount} videos`;
  const [read, setRead] = useState(false);
  const noticesRef = useRef<HTMLDialogElement>(null);
  return (
    <>
      <header className="viewer-header">
        <Link
          aria-label="memento, all albums"
          className="wordmark"
          to={`${albumList}${search}`}
        >
          <Logo />
          <span>memento</span>
        </Link>
        <div className="viewer-actions">
          <button
            aria-label={`Notifications${read ? "" : ", 1 unread"}`}
            className="icon-button notification-bell"
            onClick={() => noticesRef.current?.showModal()}
          >
            <Icon name="bell" />
            {!read && <span className="unread-dot">1</span>}
          </button>
          <span className="viewer-avatar" title="Jamie, viewer">
            J
          </span>
        </div>
      </header>
      {children}
      <dialog
        aria-labelledby="notification-title"
        className="viewer-popover notifications"
        onClick={(event) => {
          if (event.target === event.currentTarget) noticesRef.current?.close();
        }}
        ref={noticesRef}
      >
        <div className="flex items-center justify-between">
          <h2 id="notification-title">Updates</h2>
          <button
            aria-label="Close notifications"
            className="icon-button"
            onClick={() => noticesRef.current?.close()}
          >
            <Icon name="close" />
          </button>
        </div>
        <Link
          className={`notification-item ${read ? "is-read" : ""}`}
          onClick={() => {
            setRead(true);
            noticesRef.current?.close();
          }}
          to={`${base}/${preview.photoCount > 0 ? "photos" : "videos"}${search}`}
        >
          <span className="notice-icon">
            <Icon name="album" />
          </span>
          <span>
            <strong>{album.title}</strong>
            <span>{counts}</span>
            <small>Finally got these together. Enjoy!</small>
          </span>
          {!read && <span className="notice-unread" />}
        </Link>
        <button
          className="text-button"
          disabled={read}
          onClick={() => setRead(true)}
        >
          {read ? "All caught up" : "Mark as read"}
        </button>
      </dialog>
    </>
  );
}

function AlbumPage() {
  const [params] = useSearchParams();
  const { tab = "photos", mediaId } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const isAlbumList = location.pathname === albumList;
  const theme = params.get("theme") === "light" ? "light" : "dark";
  const search = location.search;
  const empty = params.get("empty");
  const photoCount = empty === "photos" ? 0 : photos.length;
  const videoCount = empty === "videos" ? 0 : videos.length;
  const cover = photoCount > 0 ? album.cover.image : videos[0].poster;
  const coverAlt =
    photoCount > 0
      ? album.cover.alt
      : "A still from the illustrated lakeside video";
  const props = { cover, coverAlt, photoCount, videoCount };
  if (tab !== "photos" && tab !== "videos")
    return <Navigate replace to={`${base}/photos${search}`} />;
  return (
    <div className="viewer-prototype" data-theme={theme}>
      <link
        href="https://fonts.googleapis.com/css2?family=Epilogue:wght@400;450;500;550;600;650;700&family=Slabo+13px&display=swap"
        rel="stylesheet"
      />
      <ViewerShell preview={props} search={search}>
        <main className={`album-main ${isAlbumList ? "album-list-main" : ""}`}>
          {isAlbumList ? (
            <>
              <h1>Your albums</h1>
              <div className="album-list-grid">
                <Link
                  className="album-preview-card"
                  to={`${base}/${photoCount > 0 ? "photos" : "videos"}${search}`}
                >
                  <img alt={coverAlt} src={cover ?? undefined} />
                  <h2>{album.title}</h2>
                  <AlbumFacts photoCount={photoCount} videoCount={videoCount} />
                </Link>
              </div>
            </>
          ) : (
            <>
              <Link className="back-to-albums" to={`${albumList}${search}`}>
                <Icon name="back" />
                All albums
              </Link>
              <AlbumHeader {...props} />
              <div className="album-tabs-row">
                <div
                  aria-label="Album media"
                  className="album-tabs"
                  onKeyDown={(event) => {
                    if (
                      !["ArrowLeft", "ArrowRight", "Home", "End"].includes(
                        event.key,
                      )
                    )
                      return;
                    event.preventDefault();
                    const next =
                      event.key === "Home"
                        ? "photos"
                        : event.key === "End"
                          ? "videos"
                          : tab === "photos"
                            ? "videos"
                            : "photos";
                    navigate(`${base}/${next}${search}`);
                    document.getElementById(`tab-${next}`)?.focus();
                  }}
                  role="tablist"
                >
                  <Link
                    aria-controls="media-panel"
                    aria-selected={tab === "photos"}
                    id="tab-photos"
                    role="tab"
                    tabIndex={tab === "photos" ? 0 : -1}
                    to={`${base}/photos${search}`}
                  >
                    <Icon name="photo" />
                    Photos<span>{photoCount}</span>
                  </Link>
                  <Link
                    aria-controls="media-panel"
                    aria-selected={tab === "videos"}
                    id="tab-videos"
                    role="tab"
                    tabIndex={tab === "videos" ? 0 : -1}
                    to={`${base}/videos${search}`}
                  >
                    <Icon name="video" />
                    Videos<span>{videoCount}</span>
                  </Link>
                </div>
              </div>
              <div
                aria-labelledby={`tab-${tab}`}
                id="media-panel"
                role="tabpanel"
                tabIndex={0}
              >
                {empty === tab ? (
                  <EmptyTab search={search} tab={tab} />
                ) : tab === "photos" ? (
                  <PhotoGallery search={search} />
                ) : (
                  <VideoGallery search={search} />
                )}
              </div>
            </>
          )}
        </main>
      </ViewerShell>
      {mediaId && empty !== tab && (
        <Lightbox mediaId={mediaId} search={search} tab={tab} />
      )}
      {import.meta.env.DEV && <ViewerPrototypeControls theme={theme} />}
    </div>
  );
}

export default function ViewerPrototype() {
  return (
    <BrowserRouter>
      <Routes>
        <Route
          element={<AlbumPage />}
          path="/prototype/viewer/album/:tab/:mediaId?"
        />
        <Route element={<AlbumPage />} path={albumList} />
        <Route element={<Navigate replace to={`${base}/photos`} />} path="*" />
      </Routes>
    </BrowserRouter>
  );
}
