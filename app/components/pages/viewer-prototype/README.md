# Viewer prototype

Throwaway UI prototype for the member-facing viewer in [specification #4](https://github.com/robinjoseph08/memento/issues/4): the album list, the album page with its header, tabs, and galleries, and the routed photo and video lightboxes with chapters. It re-opens the earlier approved viewer at `90ef56d` so it can be compared against two other structures on the real shell.

Three variants render on the real `/albums` routes, switchable with `?variant=`. The header, account menu, fonts, colors, and Tailwind tokens are the shipped ones. The notification bell is a prototype-only addition to the header for ticket 14.

Do not merge this into `master`. The winning variant gets rebuilt in production code.

## Run

```sh
pnpm install --frozen-lockfile
pnpm prototype:viewer
```

Opens <http://127.0.0.1:5175/albums?as=jamie&variant=A> signed in as Jamie, a member with Album access. The same in-memory API as the Curator prototype serves it; `?as=morgan` switches back to the Curator and `?as=alex` shows a narrower audience whose cover falls through to the picnic. The floating bar flips variants and resets the fixture.

## The variants

All three share the routed lightbox: close, title, position, download, previous and next, keyboard and swipe navigation, a date line, and an uncropped filmstrip. Videos mount a player only when opened, with a Chapters overlay that highlights the current chapter and seeks on selection. They disagree about the album page.

### A, Approved

The reference design on the real shell. An explicit All albums action, a large left-aligned heading with description and counts, the plain uncropped cover at the right (beside the title on phones), Photos and Videos tabs with count badges and a cyan underline, and justified rows with a 4px gap under day headings. Videos are cards with a title and chapter count. The album list is a grid of square covers.

Ask of it: does the shipped shell, with its Albums navigation and account menu, sit well under this header, and is the tabbed split still what a family member expects?

### B, Timeline

One chronological stream. Photos and videos share justified rows in capture order, each day has a date rail at the left that stays put while its rows scroll, and a filter row (All, Photos, Videos) in a sticky bar replaces tabs. There is no separate cover; the first day's media is the hero. The album list is a list of rows with a wide cover.

Ask of it: is a mixed stream closer to how people remember a weekend than two separate galleries? Does the date rail read better than headings between rows?

### C, Sheet

A centered gallery-wall presentation. The uncropped cover is the hero with the title beneath it, tabs are pills, and the media is a uniform square grid that fits the most on screen. Squares crop thumbnails; the lightbox shows the full image. The album list is large centered cards.

Ask of it: is density worth cropping thumbnails? Does a centered, cover-first header feel more like an occasion than the left-aligned one?

## URL state

- Variant: `?variant=A|B|C`
- Album: `/albums/lake`
- Tab or filter: `/albums/lake/photos`, `/albums/lake/videos`, and for B `/albums/lake/all`
- Lightbox: `/albums/lake/photos/p11`, `/albums/lake/videos/v01`
- Signed-in person: `?as=jamie`, kept in local storage

## Boundaries

Generated illustrations, silent clips, and fictional people only. No backend, storage, or notification delivery; the bell shows one fixed update. Publication state is ignored so the album is visible to members without publishing it first. Preview inside the Curator editor reuses the approved presentation and is not part of this comparison.
