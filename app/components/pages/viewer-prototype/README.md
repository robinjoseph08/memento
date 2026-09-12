# Viewer prototype

Master reference for the member-facing viewer in [specification #4](https://github.com/robinjoseph08/memento/issues/4): the album list, the album page with its header, tabs, and galleries, and the routed photo and video lightboxes with chapters. It supersedes the earlier viewer prototype at `90ef56d`.

The approved design was kept. Two other structures were built and compared on the same routes (a mixed chronological Timeline with a date rail, and a centered Sheet with a square grid) and dropped in favor of it. What changed is the ground it stands on: it now runs on the real shell, fonts, tokens, and primitives that shipped since the original approval.

Do not merge this into `master`; production code is rewritten from it.

## Run

```sh
pnpm install --frozen-lockfile
pnpm prototype:viewer
```

Opens <http://127.0.0.1:5175/albums?as=jamie> signed in as Jamie, a member with Album access. The same in-memory API as the Curator prototype serves it; `?as=morgan` switches back to the Curator and `?as=alex` shows a narrower audience whose cover falls through to the picnic. The floating control resets the fixture.

## The decision

- **Album list.** A grid of square, center-cropped covers with the title, counts, and date range beneath, the same card the Curator's album list uses.
- **Album header.** An explicit All albums action, then a large left-aligned heading with the description, date range, and photo and video counts beneath it. The plain, uncropped cover sits at the right, about 390px wide on desktop. On phones the smaller cover sits beside the title and the description and counts follow underneath. No fades, blur, or framing.
- **Tabs.** Photos and Videos always show with count badges and a cyan underline, 18px horizontal padding. An empty tab explains itself and links to the other tab.
- **Galleries.** Justified rows preserve every aspect ratio with a 4px gap, three landscape photos across a desktop row, one across on phones. Oldest to newest under weekday date headings with counts. Videos are cards with a play badge, the title or filename, and a chapter count.
- **Lightboxes.** Routed and full screen: close, title, position, download, previous and next, keyboard and swipe navigation, a date line, and an uncropped filmstrip with a 2px gap that scrolls the current item into view. Focus lands on the stage, and returns to the grid item on close. Videos mount a player only when opened; Chapters open temporarily over the player, highlight the current chapter, and seek on selection. No chapters is a normal state.
- **Shell.** The shipped header with its Albums link and account menu, plus the notification bell ticket 14 adds. Opening the Updates list shows the album, its counts, and the Curator's note, and marks it read.

## What consistency with the shipped app means

These are the places where the original prototype and the current codebase differed, and how this version resolves them.

- **Body type is Montserrat**, not Epilogue. Headings stay Slabo 13px. The heading scale follows the shipped `headingClass` and section sizes.
- **Colors and controls come from `styles.css` and `components/ui`.** Buttons, popovers, and dialogs are the shared primitives, so hover, focus, and pending states match the Curator screens.
- **The header keeps its Albums link.** The original viewer had none; the shipped shell has one for every signed-in person, and removing it for members would be a special case.
- **Album-list cards are the Curator's cards.** Square cropped covers on the list, uncropped media everywhere else, as `app/AGENTS.md` already records.
- **The Curator's Viewer preview should reuse this presentation.** The preview section in the Curator prototype already mirrors the header, tabs, and day sections; production should share the components rather than copy them.

## Reference captures

- [Album list](reference/albums-desktop.png)
- [Updates](reference/updates-desktop.png)
- [Album on desktop](reference/album-desktop.png)
- [Album on mobile](reference/album-mobile.png)
- [Videos tab](reference/videos-desktop.png)
- [Photo lightbox](reference/photo-lightbox-desktop.png)
- [Photo lightbox on mobile](reference/photo-lightbox-mobile.png)
- [Video with chapters](reference/video-chapters-desktop.png)

## URL state

- Album: `/albums/lake`
- Tab: `/albums/lake/photos`, `/albums/lake/videos`
- Lightbox: `/albums/lake/photos/p11`, `/albums/lake/videos/v01`
- Signed-in person: `?as=jamie`, kept in local storage

## Boundaries

Generated illustrations, silent clips, and fictional people only. No backend, storage, or notification delivery; the bell shows one fixed update. Publication state is ignored so the album is visible to members without publishing it first.
