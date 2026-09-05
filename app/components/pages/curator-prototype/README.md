# Approved Curator Album editor prototype

Throwaway interactive evidence for the [approved Curator decision on specification #4](https://github.com/robinjoseph08/memento/issues/4#issuecomment-5552928111). The combined `prototype/curator-viewer-aligned` branch contains this editor and the [approved viewer](../viewer-prototype/README.md). Do not merge it into `master`.

The compact Workbench is approved. It answers how a Curator reviews large Moments without browsing a full-size gallery or opening a form for every access change. The viewer's approved visual language remains unchanged. The decision record lives on specification #4.

Use compact Album commands, persistent section navigation, expandable Moment cards, a bounded thumbnail preview, and an adjacent access editor. Selection reveals organization actions and Moment cover selection. Access changes are directly editable, with bulk acceptance of suggestions.

Share the viewer's typography, colors, mark, base controls, and overlay treatment. The viewer's large Album header and browsing-grid density apply only to viewer presentation, not this editor.

## Run

```sh
pnpm install --frozen-lockfile
pnpm prototype:curator
```

Opens <http://127.0.0.1:5176/prototype/curator/album?section=moments>.

The original viewer remains available through `pnpm prototype:viewer`, or at `/prototype/viewer/album/photos` on the Curator server. The logo and All albums link open that viewer reference, not a new Curator Album list.

## Try it

- The cabin contains **100 photos and two videos**. It shows 24 compact thumbnails initially. Expand to all 102 items, or collapse the Moment to scan the next one.
- The access inspector is already editable. Check Sam or Taylor to grant access immediately, or click **Add all suggested** to accept both. No extra Save action. Opening the page never grants access.
- Uncheck a person to deny access at the selected scope. **Rules & exceptions** opens the detailed allow/deny/inherit form, which still uses an explicit Save action. Item overrides always take precedence.
- Select P02 and move it to the picnic to see Sam gain access. Move it to Before we left to see Alex lose access. Recommendations follow the selected media, but saved decisions do not.
- Click thumbnails to select media. Move and Split appear only when there is a selection. A single selection also offers **Set as cover**.
- Select the current Moment cover and move it. Choose its replacement before confirming.
- Split selected media or merge Moments. A merge with differing access requires explicit audience choices.
- Preview as Alex. P07 is denied, so the next qualifying Moment supplies the Album cover. Deny Alex on P13 too to see a neutral cover while the rest of the accessible media remains visible.
- Remove only Album-wide access, or use the separate confirmed action to remove all of a person's access within the Album.
- Review and publish, then unpublish. Neither action sends notifications.

Use **Undo last change** in the status bar or access inspector to reverse saved edits, including one-click grants. On mobile, the Moment's access summary opens the same controls in a sheet with Undo available inside it. The floating prototype controls expose the current fixture state, reset the study, and switch light/dark presentation.

## Reference captures

All captures use generated illustrations and fictional people.

- [Moments on desktop](reference/moments-desktop.png)
- [Moments on mobile](reference/moments-mobile.png)
- [Mobile access sheet](reference/access-mobile.png)
- [Album details](reference/details-desktop.png)
- [Album access](reference/album-access-desktop.png)
- [Viewer preview](reference/viewer-preview-desktop.png)
- [Move visibility review](reference/move-review-desktop.png)

## URL state

All examples use `/prototype/curator/album` unless noted.

- Album details: `?section=details`
- Selected Moment: `?section=moments&moment=picnic`
- Selected item: `?section=moments&moment=arrival&entry=p07`
- Mobile access sheet: add `&inspect=1`
- All media in the selected Moment: add `&media=all`
- Collapse the selected Moment: add `&collapsed=1`
- Album access: `?section=access`
- Viewer preview: `/prototype/curator/album/photos?section=preview&person=Alex`
- Preview video lightbox: `/prototype/curator/album/videos/v01?section=preview&person=Alex`
- Light theme: add `&theme=light`

Navigation and the inspected item are addressable. Bulk checkbox selection is temporary. Edits are not persisted, so reloading restores the original fixture.

## Boundaries

Only generated media from the viewer branch and fictional people are used. Face associations are fixture data, not image analysis. No Immich connection, backend, authentication, storage, notification delivery, or production API client is involved.

The viewer header, galleries, and lightbox accept optional fixture inputs for Curator preview. Their original defaults and routes remain usable. The production build excludes both prototype routes and their bundled media.

This study does not implement import/sync, identity administration, notification tooling, or the rest of the Curator application. Approval selects its organization and interactions, not its implementation shortcuts. Both viewer and Curator prototyping are complete, so implementation-ticket planning can resume from specification #4. Rewrite production code from `master`; do not merge or cherry-pick this branch.
