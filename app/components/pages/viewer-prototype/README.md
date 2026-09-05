# Approved viewer prototype

Reference-only design evidence for the [approved viewer decision on Memento specification #4](https://github.com/robinjoseph08/memento/issues/4#issuecomment-5546554501). The combined `prototype/curator-viewer-aligned` branch contains this viewer and the [approved Curator editor](../curator-prototype/README.md). Do not merge it into `master`.

## Run

```sh
pnpm install --frozen-lockfile
pnpm prototype:viewer
```

The command opens `http://127.0.0.1:5175/prototype/viewer/album/photos`.

- Album list: `/prototype/viewer/albums`
- Photos: `/prototype/viewer/album/photos`
- Videos: `/prototype/viewer/album/videos`
- A photo lightbox: `/prototype/viewer/album/photos/p01`
- A video lightbox: `/prototype/viewer/album/videos/v01`

The floating preview controls switch themes and empty-tab examples. Dark is the default. There are no competing design variants.

## Reference files

- `ViewerPrototype.tsx`: Album list, viewer shell, Album header, grids, and lightboxes.
- `viewer-prototype.css`: approved typography, colors, spacing, responsive behavior, and overlays.
- `artwork.tsx`: the selected Frames mark and interface icons. The wordmark is lowercase `memento`.
- `fixtures.ts`: fictional Album and media data.
- `reference/`: desktop, mobile, and video-lightbox captures of this version using dummy media.
- `../../ui/viewer-prototype-controls.tsx`: throwaway presentation controls, not product UI.

The Curator editor shares the typography, colors, mark, base controls, and overlay treatment. The large Album header, horizontal viewer shell, and browsing-grid density apply only to viewer presentation. The [approved Curator decision](https://github.com/robinjoseph08/memento/issues/4#issuecomment-5552928111) defines its compact editing layout. Both prototypes are approved; no further design exploration is required.

## Dummy media and boundaries

All 25 sample images, both posters, and both 30-second silent video clips in `public/viewer-prototype/` are generated landscape illustrations. They contain no photographs, people, original camera metadata, or private exports. The mixed landscape and portrait dimensions preserve the aspect-ratio cases used to approve the gallery. The clips use a slow zoom so playback and chapter seeking remain tangible.

Media files are bundled, so no image service or mounted drive is required. Google Fonts supplies Slabo 13px and Epilogue. The dedicated Vite config serves the dummy media only for this prototype command; the regular production build excludes the prototype route and these media files.

Everything is in-memory fixture behavior. There is no backend, authentication, database, persistence, or production API client. Downloads return dummy files, and the chapters are fictional. Rewrite the selected behavior under production conventions later rather than merging this prototype wholesale.
