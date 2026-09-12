# Curator album editor prototype

Master reference for the Curator album editor in [specification #4](https://github.com/robinjoseph08/memento/issues/4). It supersedes the earlier Workbench prototype at `90ef56d`.

The chosen layout is the Outline: master-detail, with every Moment and its audience visible in a left outline and the rest of the screen given to the selected Moment. Two other layouts were built and compared (the approved Workbench, and a single-column Ledger with inline access chips) and dropped in favor of it.

Everything here runs on the real `/curator/albums/:id` route with the real header, account menu, dialogs, forms, TanStack Query hooks, and Tailwind tokens. Fonts and colors are unchanged. Do not merge this into `master`; production code is rewritten from it.

## Run

```sh
pnpm install --frozen-lockfile
pnpm prototype:curator
```

Opens <http://127.0.0.1:5176/curator/albums/lake>. There is no backend: `app/main.tsx` installs an in-memory API from `stub.ts` under `--mode prototype`, and the album page swaps in the prototype editor. Both hooks compile away in ordinary builds. The floating control at the bottom right resets the fixture. Edits live in memory and reset on reload.

## The decision

- **Outline on the left.** Album details, Album access, and Viewer preview first, then every Moment as a row with its cover, date and counts, and audience stack. Hovering or focusing the stack lists everyone with access. Suggestion counts sit beside it. The active row carries the primary left bar.
- **One Moment at a time on the right.** Title, date and counts, Rename and Merge, then an access strip above a full-width grid: Allowed and Suggested side by side, Excluded and Add someone else beneath, Rules & exceptions and Undo at the top right of the strip, unlinked faces collapsed to a count, and the faces-checked line with refresh and How access works along the bottom.
- **Quick access saves immediately** with one-step Undo. Rules & exceptions opens on saved decisions only; suggestions stay suggestions until chosen individually or with Add all suggested.
- **Select mode** shows Move, Split, Set as cover, and Item access. Item access is the per-item exception editor.
- **No top-level Preview button.** Viewer preview is an outline entry. Review & publish is the only command bar action.
- **Phones drill down.** The outline is the first screen; choosing a row shows the pane with an Outline back link, instead of a side sheet.
- **Unavailable tiles are 3:2** like thumbnails, so grid rows stay even. That fix lives in `album-image.tsx` and belongs in production.

## Reference captures

- [Moments on desktop](reference/moments-desktop.png)
- [Outline on mobile](reference/outline-mobile.png)
- [A Moment on mobile](reference/moment-mobile.png)
- [Rules & exceptions](reference/rules-desktop.png)
- [Move review](reference/move-review-desktop.png)
- [Album access](reference/album-access-desktop.png)
- [Viewer preview as Alex](reference/viewer-preview-desktop.png)
- [Review & publish](reference/publish-desktop.png)

## Fixture

`fixtures.ts` builds one album, "A weekend by the lake", from the generated illustrations in `public/prototype-media`. Three Moments: At the cabin (100 photos and 2 videos, so density is realistic), Picnic & the lakeside walk (10 photos), and an untitled June 16 Moment (3 photos). Jamie has Album access. Alex is allowed on the cabin Moment but denied on its cover, P07, so previewing as Alex shows the picnic cover. Sam and Taylor are detected faces waiting as suggestions. Grandpa Joe and an unnamed face are unlinked. Priya has no access and no faces. Lee is deactivated and never appears. One cabin photo is unavailable in Immich.

## URL state

- Section: `?section=details|access|preview`, otherwise the Moments pane
- Selected Moment: `&moment=picnic`
- All media in a Moment: `&media=all`
- Mobile detail pane: `&pane=detail`
- Preview person and tab: `&person=alex&tab=videos`

## What the stub serves

Every endpoint the current app calls, with the same request and response shapes as the Go handlers, plus prototype-only endpoints for ticket 10: Album access, remove all access, Moment and item rules, publish and unpublish, and the viewer preview projection. Structural previews compute real audience changes from the fixture. None of it is an implementation prescription for ticket 10; the shapes exist so the prototype can behave.

## Boundaries

Generated illustrations and fictional people only. No Immich connection, backend, storage, or notification delivery. The Import page and People pages render but have empty or minimal data. Photo lightboxes and video playback belong to the viewer prototype.
