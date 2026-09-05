import { useEffect, useState } from "react";
import {
  createBrowserRouter,
  Link,
  RouterProvider,
  useBlocker,
  useSearchParams,
} from "react-router-dom";

import { Icon, Logo } from "../viewer-prototype/artwork";
import { AlbumDetails, ViewerPreview } from "./AlbumSections";
import { AlbumAccess, StudyDialog } from "./editors";
import {
  initialStudy,
  orderedMoments,
  pendingRecommendations,
  people,
  visibleEntries,
  type Study,
} from "./fixtures";
import { Workbench } from "./Workbench";

import "./curator-prototype.css";

// Throwaway Workbench organization, using the approved viewer's visual language.
const base = "/prototype/curator/album";
const sections = [
  ["details", "Album details"],
  ["moments", "Moments"],
  ["access", "Album access"],
  ["preview", "Viewer preview"],
];

function AlbumEditor() {
  const [params, setParams] = useSearchParams();
  const [study, setStudy] = useState(initialStudy);
  const [history, setHistory] = useState<Study[]>([]);
  const [revision, setRevision] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [message, setMessage] = useState(
    "Fictional album. Changes stay in this tab and reset on reload.",
  );
  const [modal, setModal] = useState<"publish" | "state" | null>(null);
  const blocker = useBlocker(dirty);
  const section = sections.some(([key]) => key === params.get("section"))
    ? params.get("section")!
    : "moments";
  const theme = params.get("theme") === "light" ? "light" : "dark";
  const moment =
    study.moments.find((item) => item.id === params.get("moment")) ??
    orderedMoments(study)[0];
  const audience = people.filter(
    (person) => visibleEntries(study, person).length,
  );
  const recommendations = study.moments.reduce(
    (count, item) => count + pendingRecommendations(study, item).length,
    0,
  );
  const blockers = [
    !study.title.trim() && "Add an album title.",
    !study.entries.length && "Add media.",
    study.entries.some(
      (entry) => !study.moments.some((moment) => moment.id === entry.moment),
    ) && "Assign every item to a Moment.",
  ].filter(Boolean);
  useEffect(() => {
    const unload = (event: BeforeUnloadEvent) => {
      if (dirty || history.length) event.preventDefault();
    };
    window.addEventListener("beforeunload", unload);
    return () => window.removeEventListener("beforeunload", unload);
  }, [dirty, history.length]);
  const save = (next: Study, notice: string) => {
    setHistory([...history, study]);
    setStudy(next);
    setDirty(false);
    setRevision(revision + 1);
    setMessage(notice);
  };
  const discard = (action: () => void) => {
    if (!dirty || window.confirm("Discard unsaved form changes?")) {
      setDirty(false);
      setRevision(revision + 1);
      action();
    }
  };
  const undo = () =>
    discard(() => {
      const previous = history.at(-1);
      if (previous) {
        setStudy(previous);
        setHistory(history.slice(0, -1));
        setMessage("Last saved change undone.");
      }
    });
  const sectionUrl = (key: string) => {
    const next = new URLSearchParams(params);
    next.set("section", key);
    next.delete("entry");
    next.delete("inspect");
    return `${base}${key === "preview" ? "/photos" : ""}?${next}`;
  };
  return (
    <div className="viewer-prototype curator-prototype" data-theme={theme}>
      <link
        href="https://fonts.googleapis.com/css2?family=Epilogue:wght@400;450;500;550;600;650;700&family=Slabo+13px&display=swap"
        rel="stylesheet"
      />
      <header className="viewer-header">
        <Link
          aria-label="memento, viewer reference"
          className="wordmark"
          reloadDocument
          to={`/prototype/viewer/albums?theme=${theme}`}
        >
          <Logo />
          <span>memento</span>
        </Link>
        <div className="viewer-actions">
          <span className="curator-hint">Curator</span>
          <span className="viewer-avatar" title="Morgan, Curator">
            M
          </span>
        </div>
      </header>
      <main className="curator-main">
        <header className="curator-command">
          <div className="curator-album-identity">
            <a
              aria-label="All albums, viewer reference"
              className="icon-button"
              href={`/prototype/viewer/albums?theme=${theme}`}
              title="All albums, viewer reference"
            >
              <Icon name="back" />
            </a>
            <div>
              <h1>{study.title}</h1>
              <p>
                {study.entries.filter((entry) => entry.kind === "photo").length}{" "}
                photos,{" "}
                {study.entries.filter((entry) => entry.kind === "video").length}{" "}
                videos{" "}
                <span className="publication-status">
                  {study.published ? "Published" : "Unpublished"}
                </span>
              </p>
            </div>
          </div>
          <div className="curator-command-actions">
            <Link className="curator-button" to={sectionUrl("preview")}>
              Preview as…
            </Link>
            <button
              className="action-button"
              onClick={() => discard(() => setModal("publish"))}
            >
              {study.published ? "Publication" : "Review & publish"}
            </button>
          </div>
        </header>
        <div
          className={`curator-layout ${section === "preview" ? "is-preview" : ""}`}
        >
          <aside className="curator-outline">
            <nav aria-label="Album editor sections">
              {sections.map(([key, label]) => (
                <Link
                  aria-current={section === key ? "page" : undefined}
                  key={key}
                  to={sectionUrl(key)}
                >
                  {label}
                  {key === "moments" && <span>{study.moments.length}</span>}
                </Link>
              ))}
            </nav>
            <div className="outline-summary">
              <p>
                {recommendations
                  ? `${recommendations} access suggestions`
                  : "No pending suggestions"}
              </p>
              <small>
                {study.published
                  ? "Access edits affect viewers immediately."
                  : "Only Curators can see this album."}
              </small>
            </div>
          </aside>
          {section === "moments" ? (
            <Workbench
              canUndo={history.length > 0}
              key={moment.id}
              moment={moment}
              onCancelDirty={discard}
              onDirty={() => setDirty(true)}
              onSave={save}
              onUndo={undo}
              revision={revision}
              study={study}
            />
          ) : (
            <div className="curator-section-scroll">
              {section === "details" && (
                <AlbumDetails
                  key={revision}
                  onDirty={() => setDirty(true)}
                  onSave={save}
                  study={study}
                />
              )}
              {section === "access" && (
                <AlbumAccess
                  key={revision}
                  onDirty={() => setDirty(true)}
                  onDiscard={() => setDirty(false)}
                  onSave={save}
                  study={study}
                />
              )}
              {section === "preview" && <ViewerPreview study={study} />}
            </div>
          )}
        </div>
        <footer className="curator-save-status">
          <p aria-live="polite" role="status">
            {dirty ? "Unsaved changes" : message}
          </p>
          <button
            className="text-button"
            disabled={!history.length}
            onClick={undo}
          >
            Undo last change
          </button>
        </footer>
      </main>
      <aside aria-label="Prototype controls" className="prototype-switcher">
        <button
          className="prototype-current"
          onClick={() => discard(() => setModal("state"))}
        >
          <span>Throwaway prototype</span>
          <strong>Curator Workbench</strong>
        </button>
        <span className="prototype-divider" />
        <button
          aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
          onClick={() =>
            setParams((previous) => {
              const next = new URLSearchParams(previous);
              next.set("theme", theme === "dark" ? "light" : "dark");
              return next;
            })
          }
        >
          <Icon name={theme === "dark" ? "sun" : "moon"} />
        </button>
        <button
          aria-label="Inspect prototype state"
          onClick={() => discard(() => setModal("state"))}
        >
          <Icon name="settings" />
        </button>
      </aside>
      {modal === "state" && (
        <StudyDialog
          onClose={() => setModal(null)}
          title="Curator Workbench study"
        >
          <p>
            Historical Workbench organization, approved viewer styling. The
            cabin contains 100 fictional photos and two videos to test review
            density. All media uses the bundled generated illustrations.
          </p>
          <ul className="study-scenarios">
            <li>
              Grant Sam or Taylor access in one click, or use Add all suggested.
              Undo reverses the saved change.
            </li>
            <li>
              The first 24 items are shown initially. Expand to all 102, select
              media, then move, split, or choose a Moment cover.
            </li>
            <li>
              Preview as Alex. The first Moment cover is denied, so the picnic
              cover appears.
            </li>
            <li>
              Move P02 to Before we left to review Alex losing access. Saved
              decisions remain; recommendations follow media.
            </li>
            <li>
              Use Rules & exceptions for inherited access and item overrides. No
              detected face ever grants access automatically.
            </li>
          </ul>
          <button
            className="curator-button"
            onClick={() => {
              if (window.confirm("Reset all prototype edits?")) {
                setStudy(initialStudy);
                setHistory([]);
                setRevision(revision + 1);
                setDirty(false);
                setMessage("Prototype reset.");
                setModal(null);
                setParams({ section: "moments", theme });
              }
            }}
          >
            Reset fixture
          </button>
          <details className="state-details">
            <summary>Current fixture state</summary>
            <pre>{JSON.stringify(study, null, 2)}</pre>
          </details>
        </StudyDialog>
      )}
      {modal === "publish" && (
        <StudyDialog
          onClose={() => setModal(null)}
          title={study.published ? "Published album" : "Ready to publish?"}
        >
          <p>
            {study.published
              ? "This album is available to its saved audience."
              : "Publication makes this album available to the people below. It sends no notifications."}
          </p>
          <div className="publication-audience">
            {audience.length ? (
              audience.map((person) => (
                <div key={person}>
                  <strong>{person}</strong>
                  <span>
                    {visibleEntries(study, person).length} of{" "}
                    {study.entries.length} items
                  </span>
                </div>
              ))
            ) : (
              <p>
                No one has access. You may publish now and grant access later.
              </p>
            )}
          </div>
          <ul className="readiness-list">
            <li>
              {study.entries.length} items in {study.moments.length} Moments
            </li>
            <li>No unfinished structural edits</li>
            <li>
              {recommendations} access recommendations remain. Optional to
              review.
            </li>
            <li>One video has no chapters. This is normal.</li>
          </ul>
          {blockers.map((item) => (
            <p key={String(item)} role="alert">
              {item}
            </p>
          ))}
          <div className="curator-buttons">
            <button
              className="action-button"
              disabled={!study.published && blockers.length > 0}
              onClick={() => {
                save(
                  { ...study, published: !study.published },
                  study.published
                    ? "Album unpublished. Curation and access kept."
                    : "Album published. No notifications sent.",
                );
                setModal(null);
              }}
            >
              {study.published ? "Unpublish album" : "Publish album"}
            </button>
            <button className="curator-button" onClick={() => setModal(null)}>
              Keep editing
            </button>
          </div>
        </StudyDialog>
      )}
      {blocker.state === "blocked" && (
        <StudyDialog onClose={() => blocker.reset()} title="Unsaved changes">
          <p>Save this form before leaving, or discard its changes.</p>
          <div className="curator-buttons">
            <button
              className="action-button"
              onClick={() => {
                setDirty(false);
                setRevision(revision + 1);
                blocker.proceed();
              }}
            >
              Discard and leave
            </button>
            <button className="curator-button" onClick={() => blocker.reset()}>
              Keep editing
            </button>
          </div>
        </StudyDialog>
      )}
    </div>
  );
}

const router = createBrowserRouter([
  {
    path: "/prototype/curator/album/:tab?/:mediaId?",
    element: <AlbumEditor />,
  },
]);
export default function CuratorPrototype() {
  return <RouterProvider router={router} />;
}
