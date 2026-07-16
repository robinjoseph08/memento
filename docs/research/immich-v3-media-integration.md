# Immich v3.0.3 media integration contract

Research date: 2026-07-16
Target server: Immich v3.0.3

## Decision

Immich v3.0.3 can be the portal's only media source. The portal does not need a second master copy of photos or videos. It should keep its own publication, audience, and interaction data plus a reconciled index of Immich album and asset IDs, while all thumbnails, playback, and original downloads are streamed from Immich through an authenticated portal endpoint.

The integration should use the stable album, metadata-search, asset, thumbnail, playback, and download REST endpoints with a least-privilege API key. It should **not** depend on Immich shared links, direct browser-to-Immich requests, internal timeline endpoints, or the session-only sync stream. Album and membership changes should be detected by periodic, idempotent REST reconciliation. This boundary is important because v3 deliberately removed assets from `AlbumResponseDto` and directs integrations to `POST /api/search/metadata` instead ([official v3 migration guide](https://immich.app/blog/v3-migration#album-responses--albumresponsedto)).

This is feasible, but Immich remains a runtime dependency: if Immich, its derivatives, or an external-library original is unavailable, the portal cannot serve that media unless it has introduced a separate cache or copy.

## Supported REST contract

All routes below are relative to the Immich `/api` base URL. The controllers mark these endpoints Stable since v2, and the v3.0.3 SDK is generated from the same OpenAPI contract ([Immich API generation notes](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/api.md), [v3.0.3 SDK package](https://github.com/immich-app/immich/blob/v3.0.3/packages/sdk/package.json#L1-L24)).

| Portal need | v3.0.3 operation | Required API-key permission | Integration notes |
| --- | --- | --- | --- |
| Discover curator albums | `GET /albums?isOwned=true` (`getAllAlbums`) | `album.read` | Returns the authenticated user's owned albums. It supports `id`, exact `name`, `isOwned`, `isShared`, and `assetId` filters ([album controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/album.controller.ts#L28-L36), [album query DTO](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/album.dto.ts#L47-L61)). |
| Read album metadata | `GET /albums/{id}` (`getAlbumInfo`) | `album.read` | Returns the album record, not its asset members ([album controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/album.controller.ts#L61-L69), [v3 migration guide](https://immich.app/blog/v3-migration#album-responses--albumresponsedto)). |
| List an album's assets | `POST /search/metadata` (`searchAssets`) with `albumIds: [id]` | `asset.read` | This is the supported v3 replacement for embedded album assets. Request `withExif` and `withPeople` only when needed, use `size <= 1000`, and follow `assets.nextPage`; `assets.total` is deprecated in v3.0.0 ([search controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/search.controller.ts#L30-L40), [search request and pagination schemas](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/search.dto.ts#L10-L80), [search response schema](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/search.dto.ts#L188-L207)). |
| Read one asset and its metadata | `GET /assets/{id}` (`getAssetInfo`) | `asset.read` | Includes EXIF, tags, recognized people, stack information, filenames, timestamps, dimensions, checksum, and state for an asset the key's user can access ([asset service](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/asset.service.ts#L62-L92), [asset response schema](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/asset-response.dto.ts#L61-L117)). |
| Image derivatives | `GET /assets/{id}/thumbnail?size=thumbnail|preview|fullsize` (`viewAsset`) | `asset.view` | `size=original` is deprecated; use the original endpoint. Immich may redirect between derivative sizes or to the original, so the proxy must follow redirects while retaining authentication ([media DTO](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/asset-media.dto.ts#L8-L30), [thumbnail controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/asset-media.controller.ts#L110-L160)). |
| Video playback | `GET /assets/{id}/video/playback` (`playAssetVideo`) | `asset.view` | Streams Immich's playable video and supports byte-range requests ([media controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/asset-media.controller.ts#L163-L177)). |
| One full-resolution published file | `GET /assets/{id}/original` (`downloadAsset`) | `asset.download` | Streams the original file represented by the Immich asset; `edited=true` asks for an Immich edit when available ([media controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/asset-media.controller.ts#L92-L108), [download query DTO](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/asset.dto.ts#L184-L194)). |
| Event/moment ZIP download | `POST /download/info`, then `POST /download/archive` | `asset.download` | `download/info` divides explicit asset IDs into archives and may add Live Photo motion assets; `download/archive` streams the caller-supplied IDs as a ZIP ([download controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/download.controller.ts#L16-L40), [download service](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/download.service.ts#L15-L84), [download DTOs](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/download.dto.ts#L5-L35)). |

For recipient ZIPs, the portal must derive the recipient's authorized **asset ID list** server-side, not accept an Immich album ID or an arbitrary client-supplied list. An Immich album download operates on the entire album, whereas portal access may cover only some curator-only moments within that album. `download/info` can expand an authorized Live Photo still with its motion-video asset, so the portal should treat that pair as one item: every returned archive ID must be either directly authorized or the current `livePhotoVideoId` companion of a directly authorized asset. Keep the resulting archive plan server-side and revalidate it before calling `download/archive`; if generation is deferred, store a recipient-bound, expiring plan rather than trusting IDs submitted later by the browser. Immich's archive check runs as the curator and therefore does not enforce portal audiences ([download planning and Live Photo expansion](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/download.service.ts#L15-L75), [archive authorization](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/download.service.ts#L77-L84)).

The portal should normalize Immich responses rather than forwarding JSON wholesale. `AssetResponseDto` contains `originalPath` and `libraryId`, which reveal internal storage details that recipients do not need ([asset response schema](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/asset-response.dto.ts#L68-L104)).

## Authentication and media proxy boundary

Create one API key owned by the curator's Immich account with only the permissions used by the complete connector:

- `album.read`
- `asset.read`
- `asset.view`
- `asset.download`
- `person.read`
- `face.read`

The first four permissions are sufficient for a media-only connector; `person.read` and `face.read` support the portal's separate attendance-suggestion reconciler. Both components share the same curator trust boundary, so one six-permission connector key is the simpler default. Immich allows users to generate permission-limited API keys ([official user documentation](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/command-line-interface.md#L189-L193)); the OpenAPI security scheme sends the secret in the `x-api-key` header ([v3.0.3 OpenAPI specification](https://github.com/immich-app/immich/blob/v3.0.3/open-api/immich-openapi-specs.json#L16388-L16406)). Library administration permission is unnecessary: external-library assets behave like other assets after Immich imports them, including being addable to albums ([external-libraries documentation](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L1-L9)).

The API key must remain server-side. Each portal media route should:

1. authenticate the portal recipient;
2. check the portal's published access grant for the requested asset;
3. fetch or stream the corresponding Immich route with `x-api-key`;
4. forward only safe media headers and content.

This prevents the curator-level key from reaching a browser and makes the portal, rather than Immich, the recipient authorization boundary. For video, forward `Range` and the corresponding range response headers. For thumbnail redirects, follow the redirect inside the proxy so the redirected request still receives the API key. Immich's file responses use private caching, with derivatives eligible for a one-day private cache plus stale revalidation; the portal can preserve or shorten that policy without storing another master ([Immich file response utility](https://github.com/immich-app/immich/blob/v3.0.3/server/src/utils/file.ts#L34-L67)).

Do not use public/shared links as the connector credential. They express Immich's album/asset sharing model rather than the portal's per-recipient publication model, and shared-link routes accept their own key/slug authentication in addition to having different metadata/download settings ([shared-link API model](https://api.immich.app/endpoints/shared-links/getSharedLinkById)).

## Album discovery and source reconciliation

### Initial and recurring discovery

Poll `GET /albums?isOwned=true`. For each returned album, persist at least:

- Immich album ID;
- name, description, thumbnail asset ID, and sort order;
- `createdAt` and `updatedAt`;
- `assetCount`, `startDate`, `endDate`, and `lastModifiedAssetTimestamp`;
- portal disposition: unreviewed, ignored, or linked to an event.

These are fields of `AlbumResponseDto` in v3.0.3 ([album response schema](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/album.dto.ts#L89-L135)). Keep ignored albums in the index so later polls do not return them to the curator inbox.

An album-level timestamp or count is only a hint, not a complete membership cursor. `lastModifiedAssetTimestamp` is calculated as the maximum `asset.updatedAt` among current members, and `assetCount` is the current count; an equal-count remove/add or membership change involving older assets need not produce a unique monotonic album-membership marker ([album metadata query](https://github.com/immich-app/immich/blob/v3.0.3/server/src/queries/album.repository.sql#L113-L131)). Therefore correctness requires periodic paginated reconciliation of every linked album's asset-ID set via `POST /search/metadata`, even if album summary fields appear unchanged.

A pagination pass is not an atomic snapshot: metadata search uses offset pages ordered by `fileCreatedAt`, so concurrent membership changes or equal timestamps can shift or duplicate rows between requests ([metadata search pagination](https://github.com/immich-app/immich/blob/v3.0.3/server/src/repositories/search.repository.ts#L197-L206)). Treat each pass as a candidate set: de-duplicate IDs, read album summary fields before and after, and discard/retry if they change during the pass. Before staging removals, require two consecutive passes with identical ID sets; this prevents a transient omission from becoming a plausible removal. Additions can enter the coalescing staged update after a validated pass because they remain private until publication.

For a small family library, a simple schedule such as every 5–15 minutes for album discovery and linked-album reconciliation is reasonable. It should be one background synchronization process, not polling per recipient or per open browser. The only published request-size constraint relevant here is the metadata search maximum of 1,000 results per page, so the connector must paginate and should also use bounded concurrency, timeouts, exponential backoff, and jitter rather than assuming unlimited server capacity ([search request schema](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/search.dto.ts#L53-L80)). Immich v3.0.3 does not publish a throughput quota for these routes; that absence is not a performance guarantee.

### Coalescing staged updates

Reconciliation should compare the latest validated Immich asset-ID set with the event's last published source revision. Keep one mutable staged update per event, derived from the net difference:

- newly present asset IDs are pending additions;
- no-longer-present IDs are pending removals/source-unavailable items;
- existing IDs whose relevant source fingerprint changed are pending updates;
- changes arriving before publication recalculate that same staged update rather than creating another batch.

A useful source fingerprint is `(asset id, updatedAt, fileModifiedAt, checksum, thumbhash, relevant EXIF fields)`. Immich defines `fileModifiedAt` as the underlying filesystem modification time, `updatedAt` as the database record's last update time, and `thumbhash` as a cache-busting input ([asset response schema](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/asset-response.dto.ts#L25-L47), [asset timestamps and checksum](https://github.com/immich-app/immich/blob/v3.0.3/server/src/dtos/asset-response.dto.ts#L78-L104)). Recomputing a net diff also makes add-then-remove before publication disappear instead of leaving contradictory staged records.

If an album disappears from `GET /albums`, mark its link source-missing and alert the curator; do not silently delete the portal event or its audience history. If an already-published asset disappears, remove it from future portal listings or visibly mark it unavailable according to a product policy, because a no-copy portal cannot continue serving a missing source file.

## Why polling, not webhooks or Immich sync

Immich v3.0.3 has no public outbound-webhook registration contract in its OpenAPI specification. Its Socket.IO client event map includes upload, asset update/trash/delete, and other UI events, but no album or album-membership event, so it is not a complete source for the changes this portal must observe ([v3.0.3 WebSocket event map](https://github.com/immich-app/immich/blob/v3.0.3/server/src/repositories/websocket.repository.ts#L24-L50)). Socket events can at most be an optimization that wakes a reconciler; periodic REST reconciliation remains authoritative.

`POST /sync/stream` does carry album, album-to-asset, asset, update, and delete records and is marked Stable for the mobile sync implementation. However, v3.0.3 explicitly rejects all sync endpoints when authentication is an API key, retains audit data for only about 30 days, and forces a full reset after an old checkpoint ([sync service session restriction and handlers](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/sync.service.ts#L83-L203), [sync retention/reset](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/sync.service.ts#L205-L234)). Using it would require the portal to hold and maintain an Immich login session rather than the intended least-privilege API key. That additional credential and checkpoint machinery is not justified for a small, single-curator portal when the stable metadata search can reconcile complete state.

## External-library behavior and ID stability

Album and asset IDs are UUID primary keys and are safe portal references for the lifetime of their Immich records ([album table](https://github.com/immich-app/immich/blob/v3.0.3/server/src/schema/tables/album.table.ts#L16-L37), [asset table](https://github.com/immich-app/immich/blob/v3.0.3/server/src/schema/tables/asset.table.ts#L63-L102)). They are not durable content identities across delete/re-import operations:

- Editing/replacing a file at the same external-library path causes Immich to rescan metadata for the existing record after its modification time changes ([library scan implementation](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/library.service.ts#L575-L617)).
- Moving a file within an external library is currently treated as removing one asset and adding a new one, losing Immich-only metadata; the moved asset therefore receives a new identity ([external-libraries caution](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L11-L15)).
- Removing an external file moves its asset to trash on rescan; after 30 days it is deleted and Immich-only metadata is lost. Restoring a path after it has fallen out of an import path adds it as a new file ([external-libraries deletion and import-path behavior](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L7-L13), [import paths](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L23-L27)).
- Deleting an external library deletes all of its Immich assets ([external-library deletion](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L79-L87)).

Consequently, the portal should use Immich UUIDs as foreign references but treat an ID change as a real source remove/add. Checksums and paths can help the curator recognize a moved item, but the portal should not automatically transfer a publication grant to a new asset ID without an explicit, tested rule.

Changes on the NAS are not visible to the portal until Immich notices them. Immich's filesystem watcher is experimental and likely not to work on network drives; the documented fallback is a configurable periodic library scan, whose default scheduled cadence is daily ([external-library watcher](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L63-L81)). The end-to-end freshness bound is therefore **Immich scan delay plus portal reconciliation delay**. Configure the Immich library scan cadence to match the desired publishing workflow before tuning portal polling.

## No second master copy

External libraries keep the source media outside Immich; Immich imports database records and generates its own metadata/derivatives while the external files stay on the mounted filesystem ([external-libraries overview](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L1-L9), [external-library setup with read-only mounts](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/guides/external-library.md#L7-L18)). The portal can therefore remain metadata-only:

- store Immich album/asset IDs and selected normalized metadata;
- store portal events, curator-only moments, grants, comments, favorites, and publication revisions;
- proxy Immich derivatives and originals as streams;
- rely on normal browser/private derivative caching, optionally adding a bounded evictable derivative cache.

A derivative cache is operationally optional and is not a master copy. Persisting original bytes in portal storage would create a second retention and revocation system and is unnecessary for the stated workflow.

## Compatibility and operational limits

Pin `@immich/sdk` to `3.0.3` (or generate a client from the v3.0.3 OpenAPI file) rather than consuming an unbounded latest version. On startup, call the public stable `GET /server/version` endpoint and fail health checks on an unsupported major version ([server version controller](https://github.com/immich-app/immich/blob/v3.0.3/server/src/controllers/server.controller.ts#L76-L84)). Run connector contract tests against a disposable Immich instance before every Immich upgrade.

This is not theoretical caution: Immich's official v3 migration guide documents breaking response and endpoint changes for third-party integrations, including removal of assets from album responses and removal of legacy sync endpoints ([official v3 migration guide](https://immich.app/blog/v3-migration#endpoints)). The routes recommended in this note are Stable in v3.0.3, but Stable does not mean immutable across future major releases.

The principal operational limits are:

- metadata search pages contain at most 1,000 assets;
- bulk downloads may be split into multiple archives, defaulting in the implementation to roughly 4 GiB targets ([download service](https://github.com/immich-app/immich/blob/v3.0.3/server/src/services/download.service.ts#L45-L85));
- external-library refresh and thumbnail/transcode jobs are asynchronous, so newly discovered media may briefly have incomplete derivatives ([external-library setup guide](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/guides/external-library.md#L42-L55));
- aggressive Immich caching can make an externally refreshed asset appear stale for a time ([external-library caching caution](https://github.com/immich-app/immich/blob/v3.0.3/docs/docs/features/libraries.md#L17-L21));
- no-copy serving couples portal availability and throughput to Immich, the NAS, and Immich's generated media.

## Recommended architecture boundary

Adopt the custom portal over Immich as this contract:

```text
Lightroom export -> NAS external library -> Immich scan/index/derivatives
                                               |
                         server-side API-key connector + reconciler
                                               |
                         portal metadata DB and publication workflow
                                               |
                   recipient-authenticated media proxy -> family browser
```

Immich owns originals-by-reference, media processing, albums, asset metadata, thumbnails, playback, and full-resolution retrieval. The portal owns discovery disposition, event/moment structure, staged publication, people/audience rules, recipient accounts, authorization, notifications, comments, favorites, and download selection. Recipient accounts never need to exist in Immich.

That division supports the proposed product without duplicating master media. The main implementation obligations exposed by the research are a robust paginated reconciler, explicit handling of source removals/ID churn, and an authorization-checking streaming proxy that never exposes the curator's API key or Immich's raw internal metadata.
