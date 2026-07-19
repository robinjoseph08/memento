# Family Photo Sharing

This context covers the private publication of one curator's photos and videos to selected family members.

## Language

### People, roles, and discovery

**Curator**:
The sole Person with authority to publish media and control who may access it. Curator is a role on a Person, and that same Person may also be a Recipient rather than having a separate identity.
_Avoid_: Publisher, admin, photographer

**Recipient**:
A Person who has been invited to receive access to published media but does not publish or manage sharing. Each Recipient is exactly one Person with one case-insensitively unique login email; Invitation, email, Session, and access changes do not create or replace that Person.
_Avoid_: User, viewer, contributor

**Eligible Recipient**:
A Recipient whose access is neither suspended nor revoked. Completing Onboarding is not required for Audience approval, but Publication and activity notifications wait until Onboarding is complete.
_Avoid_: Active user, enabled user

**Person**:
A family member who may attend a Moment, whether or not they are a Recipient. A Person persists independently of invitation status, login access, and email address. Only the Curator may create, change, archive, or merge People and Family relationships.
_Avoid_: Contact, profile, face

**Attendance**:
The curator-confirmed people who were present at a moment; face detections may suggest people but are not authoritative.
_Avoid_: Detected faces, appearances

**Interest list**:
The People explicitly chosen by a Recipient, or by the Curator on that Recipient's behalf, whose Attendance should cause that Recipient to be suggested for a Moment. Choices are limited to People visible through shared Visibility circles. Either may edit the list, and every change is attributed to the Person who made it and retained in an audit history. Changes to Family relationships or a Family branch may provide new choices but never alter the list automatically; when a Recipient and chosen Person no longer share any Visibility circle, that choice is deactivated without erasing its history and remains inactive until explicitly reselected after visibility returns. It influences an Audience proposal but never grants access.
_Avoid_: Permissions, subscriptions, access list

**Family relationship**:
An explicit parent-child, partner, or sibling connection between People. Partner connections may be current or former; sibling connections may be recorded even when their shared parents are absent. All annotate People choices; parent-child and current-partner connections contribute to a Family branch, while sibling and former-partner connections do not.
_Avoid_: Account relationship, inferred relationship

**Visibility circle**:
A Curator-managed, overlapping set of People that determines whom a Recipient may discover and choose for their Interest list. A Recipient may discover the union of People in every circle containing their own Person; membership is not transitive across circles and never grants media access.
_Avoid_: Bubble, group, Audience

**Family branch**:
A Person's current partners, every descendant, and every descendant's current partners recursively through all generations. It provides relationship-annotated choices for that Recipient's Interest list but never adds them without explicit opt-in. Siblings and their descendants are not included automatically.
_Avoid_: Immediate family, household

### Recipient access

**Invitation**:
A Curator-issued, single-use offer sent to a Person's login email that expires after fourteen days and begins Recipient access. It is explicitly accepted and may be revoked or reissued without replacing the Person.
_Avoid_: Share link, login link

**Onboarding**:
The initial Recipient setup between Invitation acceptance and the Recipient's explicit completion. Completion is required for all access and delivery of published Media items and for Publication or activity notifications, and a later email change does not repeat it.
_Avoid_: Registration, account creation

**Suspension**:
A reversible pause of Recipient access that invalidates every Session. Lifting it restores access from still-valid Audiences after the Recipient signs in again.
_Avoid_: Revocation, sign-out

**Revocation**:
The permanent end of a Recipient's current access that invalidates every Session and every existing Audience entry for authorization. Reinviting the same Person preserves their history but does not restore earlier access without explicit Curator approval.
_Avoid_: Suspension, withdrawal

**Session**:
A separately revocable authorization for one Recipient on one browser or device, established after email verification. It never replaces the Recipient or grants access beyond that Recipient's valid Audiences.
_Avoid_: Account, Audience, device

**Trusted-device session**:
A Recipient Session that remains valid until one year of inactivity and may continue indefinitely while the device remains active.
_Avoid_: Permanent login, account

**Public-computer session**:
A Recipient Session chosen for a public or shared computer whose browser credential is discarded when the browser session ends and whose server-side authorization expires after twelve hours.
_Avoid_: Trusted device, incognito mode

### Media and publication

**Source album**:
An Immich album tracked by the portal as media provenance; when the Curator chooses to draft it, it maps by default to one Event but may be combined, divided, ignored, or partially unpublished without changing Immich. An ignored Source album remains ignored and tracked until the Curator restores it, and one absent from Immich becomes Source missing.
_Avoid_: Event, published album

**Source missing**:
The state of a Source album or Media item that the portal still knows about but Immich can no longer serve. Its record remains available only to the Curator for relinking or correction, while its published representation stays in Recipient listings until a correction Publication but cannot deliver Media.
_Avoid_: Deleted, withdrawn

**Media item**:
A portal-tracked photo or video backed by an Immich asset that may appear in multiple Events, with item-level Audience entitlement equal to the union of their approved Audiences. A Curator-confirmed relink replaces that backing asset while preserving portal identity, Event placement, Audiences, stable URL, Comments, Favorites, and history.
_Avoid_: Asset, file

**Event**:
A narrative container for related Media items drawn from one or more Source albums, possibly through Moments with different Audiences. Its presentation metadata becomes portal-owned after initialization from Immich and cannot be overwritten by source changes; during a split, the Curator designates which result retains its identity while the others receive new identities.
_Avoid_: Album, gallery

**Moment**:
A curator-only part of an Event that has one approved Audience once published; Recipients see one filtered Event without its Moment boundaries. Media items default to local-capture-date proposals using a curator-chosen timezone for unzoned timestamps, but the Curator may merge, split, or manually place items with no usable date.
_Avoid_: Sub-album, segment

**Loose item**:
A Media item shared independently rather than through an Event.
_Avoid_: One-off, loose photo

**Archive download**:
A single-use ZIP of original Media items, bound for fifteen minutes to one Recipient and Session, containing either every item they may access in an Event or an explicit subset. Its selection is reauthorized before delivery and excludes inaccessible Media items and source-library paths.
_Avoid_: Album export, backup

**Audience proposal**:
A draft set of Eligible Recipients for a Moment or Loose item. For a Moment, the system derives it by intersecting confirmed Attendance with Interest lists; the Curator may add or exclude Eligible Recipients for either kind of item, and those overrides persist through draft recalculation. It becomes an Audience only after Curator approval. The Curator never appears in a proposal because Curator authority already provides access.
_Avoid_: Automatic sharing, recipient list

**Audience**:
A Curator-approved snapshot, which may be empty, of the Eligible Recipients allowed to access one Moment or Loose item and the sole source of item-level media access. It never recalculates from later changes to Attendance, Family relationships, Interest lists, or Visibility circles; Suspension disables its access temporarily, while Revocation invalidates its existing entry permanently.
_Avoid_: Members, invitees

**Publication**:
The Curator's atomic approval that makes an Event or its entire Staged update visible to its reviewed Audiences. Every Moment requires an approved Audience, including an explicitly approved empty Audience for curator-only material.
_Avoid_: Sync, import

**Staged update**:
The single coalescing net change to a published Event that remains private until the Curator publishes it. It may include source additions and removals, optional source metadata suggestions, Curator edits, Moment assignment or structure, ordering, and Audiences; changes that cancel before Publication leave no residue.
_Avoid_: Live sync, pending upload

**Withdrawal**:
The Curator's immediate revocation of Recipient access to an Event, Moment, or Media item without erasing its identity, interactions, or publication history. Restoration requires a new Publication with freshly reviewed Audience snapshots.
_Avoid_: Delete, source missing, unpublish

### Notifications and interactions

**Notification preference**:
A Recipient's choice to receive Publication emails immediately, in a weekly digest, or not at all. Publication and activity notifications do not begin until the Recipient completes Onboarding.
_Avoid_: Access preference, subscription

**Favorite**:
A recipient's personal selection of a photo or video, visible to that recipient and the curator but hidden from other recipients.
_Avoid_: Like, reaction, public favorite

**Comment**:
A message on a photo or video, visible only to the curator and recipients who can access that item.
_Avoid_: Event comment, public comment
