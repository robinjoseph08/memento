# Immich v3.0.3 person and face identity behavior

Research for [Validate Immich v3.0.3 person and face identity behavior](https://github.com/robinjoseph08/memento/issues/3). This is pinned to Immich `v3.0.3` (commit [`cd308ad`](https://github.com/immich-app/immich/tree/cd308ad93093735135f99d85ce6980c8e93df231)); later releases may behave differently.

## Conclusion

Immich v3.0.3 is a feasible source for **advisory attendance suggestions**. The portal can use supported APIs to list Immich people, identify the people associated with an asset, retrieve each face's ID and bounding box, and search assets by person or album. The portal should link a portal person primarily to an Immich **person ID** (the face cluster), not to one face ID, and retain several observed face/asset anchors only to help repair that link. Immich's API models a person as a collection of faces, gives both people and faces UUIDs, and returns the nested person for every face on an asset. ([person/face response schemas](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/person.dto.ts#L61-L109), [face lookup implementation](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L126-L133))

Neither identifier is a permanent domain identity. A person ID survives normal edits and face reassignment, and the destination ID survives a merge, but the merged source person is deleted. A forced facial-recognition rerun can delete and recreate person rows; a forced face-detection rerun can delete and recreate machine-detected face rows. Regular face detection attempts to preserve a face ID by matching the new bounding box to the old face, but replaces the ID when it cannot match. ([merge implementation](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L555-L615), [forced jobs](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L267-L278), [face matching/replacement](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L323-L365), [forced reclustering](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L401-L440))

Therefore, a face match must never itself grant portal access. It may suggest that a portal person attended; the curator-confirmed attendance list remains authoritative. A broken or ambiguous Immich mapping should suppress that suggestion until the curator repairs it.

## Supported identifiers and lookups

| Need | Supported v3.0.3 API | Useful result |
| --- | --- | --- |
| Reconcile all face clusters | `GET /people?withHidden=true&page=…&size=…` | Person UUID, name, hidden/favorite state, and `updatedAt`. The endpoint is documented as stable, paginated, and can include hidden people. ([controller](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/controllers/person.controller.ts#L49-L68), [request/response schemas](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/person.dto.ts#L51-L87)) |
| Get people on one asset cheaply | `GET /assets/{id}` | A de-duplicated `people` array built from that asset's faces. It contains person IDs but not face IDs. ([asset lookup](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/asset.service.ts#L62-L92), [people mapping](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/asset-response.dto.ts#L165-L179)) |
| Inspect exact faces on an asset | `GET /faces?id={assetId}` | Face UUID, bounding box/dimensions, source type, and nullable nested person. The endpoint requires `face.read` and access to the asset. ([controller](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/controllers/face.controller.ts#L34-L42), [schema](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/person.dto.ts#L96-L109)) |
| Batch assets in an event/album or find assets containing people | `POST /search/metadata` with `albumIds`, `personIds`, and optionally `withPeople: true` | Paginated assets filtered by album/person, with person data when requested. ([search schema](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/search.dto.ts#L10-L80), [stable endpoint](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/controllers/search.controller.ts#L30-L40)) |
| Read one person | `GET /people/{id}` | Current person record or a not-found response once the record no longer exists. ([controller](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/controllers/person.controller.ts#L94-L103)) |

The standard person response does **not** expose its feature-face ID; it exposes the person UUID and thumbnail path. The separate sync representation does include `faceAssetId`, but the portal does not need that presentation choice as its identity key. ([standard response](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/person.dto.ts#L61-L87), [sync response](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/sync.dto.ts#L334-L347))

Use a least-privilege curator-owned API key for ordinary reconciliation. Immich v3.0.3 lets the owner restrict an API key's permissions, and these reads require `album.read`, `asset.read`, `person.read`, and `face.read`; serving the published files will separately require the appropriate asset view/download permissions. ([official API-key documentation](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/docs/docs/features/command-line-interface.md#L189-L193), [album/asset permission values](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/enum.ts#L121-L150), [face permissions](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/enum.ts#L166-L169), [person permissions](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/enum.ts#L209-L215))

## Stability by operation

### Rename, hide, and ordinary edits

Renaming, changing birth date, favoriting, hiding, changing color, or choosing a feature photo updates the existing person row, so its person ID remains the same. Hiding is an `isHidden` flag; it is not deletion or unassignment. The official feature documentation describes hiding as removing the person from the Explore/detail UI, while `withHidden=true` allows the portal to keep reconciling that record. ([update implementation](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L190-L220), [person table](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/schema/tables/person.table.ts#L33-L68), [official feature actions](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/docs/docs/features/facial-recognition.md#L19-L29))

### Face reassignment

Reassigning a face updates only that face row's `personId`; the face ID and destination person ID remain unchanged. The source person may later be removed by the cleanup job if it has no remaining faces. Immich exposes both one-face reassignment and bulk reassignment APIs, but the portal only needs to observe the result. ([single reassignment](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L109-L124), [database update](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/person.repository.ts#L300-L309), [empty-person cleanup](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L260-L265))

### Person merge

For `POST /people/{id}/merge`, the path `{id}` is explicitly the destination and **survives**. Each ID in the request body is a source: Immich moves all of its faces to `{id}`, copies a source name/birth date only when the destination lacks one, and then deletes the source row. Face IDs survive because the implementation updates their `personId` rather than recreating them. The API returns per-source success/failure, but there is no persistent old-person-to-new-person redirect. ([endpoint contract](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/controllers/person.controller.ts#L190-L204), [merge implementation](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L555-L615), [bulk face update](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/person.repository.ts#L81-L91))

### Delete

Deleting a person deletes the person row and thumbnail. The face table's foreign key uses `ON DELETE SET NULL`, so its face rows survive unassigned unless separately deleted; the deleted person ID no longer resolves. A delete audit row records the old person ID for sync clients but not a replacement ID. ([person deletion](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L247-L257), [face foreign key](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/schema/tables/asset-face.table.ts#L40-L55), [delete audit](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/schema/functions.ts#L195-L206))

Deleting a face either sets `deletedAt` (ordinary delete) or physically deletes the face row (`force=true`); normal face queries exclude deleted faces. Deleting the asset physically removes its face rows through the face table's `ON DELETE CASCADE` asset foreign key. ([delete-face behavior](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L700-L704), [repository operations](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/person.repository.ts#L514-L526), [face query filtering](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/person.repository.ts#L229-L241))

### Reprocessing

A normal face-detection run compares new and existing bounding boxes. It reuses a matching face row when intersection-over-union is greater than `0.5`, creates a new UUID for an unmatched detection, and deletes machine-learning faces that are no longer matched. Thus face IDs are useful repair anchors, not permanent identities. ([matching code](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L323-L365))

The destructive maintenance cases are stronger:

- Forced **face detection** deletes every machine-learning face, cleans up people left without faces, and redetects; both face IDs and affected person IDs can change. ([forced detection](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L267-L297))
- Forced **facial recognition** keeps the face rows but unassigns every machine-learning face, deletes people left without faces, then reclusters all faces. Face IDs generally remain, while affected person IDs can change. ([forced recognition](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/person.service.ts#L401-L456))
- Incremental recognition intentionally preserves earlier clusters as new assets arrive, so ordinary ingestion is much less disruptive than either forced operation. ([official algorithm notes](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/docs/docs/features/facial-recognition.md#L45-L63))

Immich does not document either ID as a forever-stable external identity, and these code paths demonstrate why the portal must be able to relink them.

## Change detection

Immich v3.0.3 has a supported `POST /sync/stream` JSON-lines endpoint with checkpoints. The `PeopleV1` and `AssetFacesV2` streams include person/face upserts plus separate deletion records, and face upserts carry `assetId`, `personId`, face ID, bounding box, visibility, and deletion state. A checkpoint older than 30 days requires a reset, while audit rows are pruned after 31 days. ([sync endpoint](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/controllers/sync.controller.ts#L20-L37), [sync schemas](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/sync.dto.ts#L334-L375), [people/face stream handlers](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/sync.service.ts#L818-L849), [reset and retention](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/sync.service.ts#L205-L235))

However, sync checkpoints belong to an authenticated **session**, and the service explicitly rejects API keys. It is designed for the mobile app. Maintaining an Immich login session inside the portal adds credential/session lifecycle coupling, so this should not be the MVP integration. ([session requirement](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/sync.service.ts#L83-L102), [stream requirement](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/services/sync.service.ts#L132-L159))

The authenticated websocket is also insufficient as the source of truth. Its public client-event map has upload/asset changes and person-thumbnail completion, but no event for a person merge, person deletion, face reassignment, or generic face/person update. ([client event map](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/websocket.repository.ts#L31-L50))

**MVP recommendation:** reconcile with a restricted API key on a schedule and whenever the curator opens/reviews an event. Fetch all people with `withHidden=true` to detect changed/missing mappings, and fetch the relevant album's assets with `withPeople=true` to calculate suggestions. Use `GET /faces?id=…` only for repair evidence or face-level UI. A full reconciliation is acceptable for a one-curator family library and catches deletions/merges that timestamp-only polling could miss. Revisit the sync stream only if measured scale makes polling materially expensive.

## External-library implications

External-library assets otherwise behave like regular Immich assets, so they participate in preview generation, albums, and face detection. The detection job selects any non-hidden, non-deleted asset with a preview and does not exclude external libraries. ([official external-library behavior](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/docs/docs/features/libraries.md#L1-L9), [face-job selection](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/asset-job.repository.ts#L178-L191), [detect-face queue](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/repositories/asset-job.repository.ts#L439-L445))

The material identity hazard is filesystem movement/deletion. In v3.0.3, moving a file within an external library makes Immich treat it as a new asset and lose Immich-only metadata; moving it outside an import path removes it and moving it back adds it as new. A missing file is trashed on rescan and is removed after 30 days, taking its Immich metadata with it. A new asset means a new asset ID and newly detected face rows, so stored asset/face anchors can break even when the media still looks familiar. ([official warning](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/docs/docs/features/libraries.md#L7-L27))

For this Lightroom-to-NAS workflow, keep exported file paths stable after Immich imports them. The portal should record the Immich asset ID plus durable corroborating media attributes already available from the API, especially checksum and original path/name; those attributes are repair evidence, not authorization keys. The asset response exposes a checksum, original path/name, and capture/update timestamps. ([asset response fields](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/asset-response.dto.ts#L80-L105), [mapped fields](https://github.com/immich-app/immich/blob/cd308ad93093735135f99d85ce6980c8e93df231/server/src/dtos/asset-response.dto.ts#L215-L236))

## Safe portal mapping and repair strategy

Use portal-owned identity and explicit mapping state:

```text
PortalPerson
  id                         # authoritative family-domain identity

ImmichPersonLink
  portal_person_id
  immich_owner_id
  immich_person_id           # nullable; current cluster ID
  state                      # linked | needs_review | unlinked
  last_seen_at
  last_seen_name             # display/repair evidence only

ImmichFaceAnchor             # small rotating sample, not every face forever
  portal_person_id
  immich_face_id
  immich_asset_id
  asset_checksum
  bounding_box
  last_seen_person_id
  last_seen_at
```

The repair loop should be conservative:

1. Reconcile all current people, including hidden people. A still-present mapped person ID remains linked; name changes do not alter identity.
2. For each moment under review, derive people suggested for attendance from the current person IDs returned on that moment's asset subset. De-duplicate by portal person.
3. If a mapped person ID disappears, mark the link `needs_review` and suppress suggestions from it. Do not map by name.
4. Inspect surviving face anchors. If several known face IDs now consistently point to one current person ID, show that person as a high-confidence **repair proposal** and explain that a merge/recluster likely occurred. The curator confirms the relink.
5. If face IDs also disappeared, use asset checksum/path and approximate bounding boxes to present candidates. Never auto-relink from this weaker evidence; a forced detection run or external-file move can invalidate both sides.
6. If anchors split across multiple Immich people, present the split for manual choice. This can legitimately follow a curator face reassignment.
7. Keep old link/anchor history for diagnosis, but only one curator-confirmed current Immich person link contributes suggestions.

This model makes an Immich merge pleasant without making it dangerous: the destination often becomes obvious from the surviving face anchors, yet no source deletion can silently redirect a family member's access. It also fits the product's privacy rule: detected faces propose attendance; only curator-confirmed attendance and curator-approved audiences create recipient access.

## Decision for the Wayfinder map

Proceed with Immich as the face-suggestion source. Model the portal's `Person` independently and give it a curator-maintained, repairable link to one Immich person/face cluster. Use the standard read APIs and scheduled/on-demand reconciliation for the MVP. Store a few face/asset anchors for explaining and repairing merges or reclustering, but never use Immich identity changes to grant access automatically.
