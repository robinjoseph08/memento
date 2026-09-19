# Memento

Memento publishes selected photos and videos from one Immich library to invited friends and family.

## Language

**Album**:
A reviewed, viewer-facing collection of related photos and videos imported from one Immich album. Its description comes directly from Immich, while photos and videos are presented separately.

**Media Item**:
Memento's record of one photo or video asset from Immich. Source metadata, chapters, availability, and an optional video title remain consistent wherever the Media Item appears.
_Avoid_: Asset

**Album Entry**:
A Media Item's inclusion in one Album and exactly one Moment. Album-specific access applies to the Album Entry rather than globally to the Media Item.

**Excluded Media**:
An Album Entry a Curator keeps out of one Album while its asset stays in the Immich album. It keeps its identity, Access Decisions, and announcement history, appears in no Moment, and is never offered again by a check for changes. Keep out excludes it and Add back returns it to a reviewed Moment.
_Avoid_: Hidden, Removed, Deleted

**Ignored Album**:
An Immich album a Curator keeps off the import list because it should never become an Album, such as a phone's synced Recents. It is listed apart on the import page, and Restore offers it for import again. Ignoring changes nothing in Immich and nothing already imported.
_Avoid_: Hidden, Blocked, Excluded

**Moment**:
A nonempty Curator-only portion of an Album whose media was captured around the same time and usually involves the same people. Moments default to capture days but may be split, merged, or span midnight. Each Moment has a Curator-selected Album Entry as its cover; viewer Album covers are derived from accessible Moment covers through the Album's Cover Order rather than stored separately.
_Avoid_: Access Set, Day Group, Chapter

**Publication**:
A Curator's explicit act of making an Album available according to its Access Decisions. Publication does not send notifications.
_Avoid_: Notification, Announcement

**Chapter**:
A read-only navigation segment extracted from a video's original file. Chapters do not control access and are distinct from Moments.

**Person**:
A real human known to Memento. A Person may exist before gaining login access, may correspond to zero or more face records in Immich, and may use one linked face as an avatar.
_Avoid_: User, Recipient, Immich Person

**Curator**:
A Person who can organize Albums, manage access, and invite people. One Installation may have multiple Curators.

**Preauthorization**:
A Curator's approval for a specific email address to join Memento when its owner signs in with Google.
_Avoid_: Allowlist

**Invitation**:
A proactive email that introduces a preauthorized Person to Memento and starts the standard onboarding flow.
_Avoid_: Preauthorization

**Access Request**:
A verified request either to join Memento or to view an inaccessible Album. It records the requester and attempted Album when known but grants no access by itself.

**Access Recommendation**:
A suggestion to share a Moment with a Person detected in its current media. Recommendations follow Moment membership and never change access without a Curator's approval.

**Access Decision**:
A Curator's explicit access choice for a Person. Albums may allow access, Moments and Album Entries may allow or deny it, and missing decisions inherit from broader scopes before defaulting to denied.

**Cover Order**:
A Curator's ordered list of preferred Moments for one Album's cover. Each viewer sees the cover of the first Moment in the Cover Order they can access; when none applies, the earliest accessible Moment cover in capture order is used, so an Album with an empty Cover Order behaves as if none existed.
_Avoid_: Priority, Ranking, Pin, Cover Override

**Viewing Group**:
The People who can see exactly the same Moment covers in one Album. Viewing Groups are derived from Access Decisions whenever a Curator reviews an Album's cover; they are never stored, named, or shown to viewers. A Person who can see no Moment cover belongs to no Viewing Group.
_Avoid_: Cohort, Access Set, Segment

**Unannounced Change**:
An Album or Album Entry that became visible to a Person after that Person's notification baseline. Completing Onboarding, approving an Update Notification, or dismissing selected changes advances the baseline, independently of email or Push delivery.

**Update Notification**:
A Curator-approved summary of one or more Unannounced Changes for a Person, always readable inside Memento. Email and Push are optional delivery channels for the same notification, and neither creates a notification of its own. Email respects the Person's email preference, and Push follows the phone's own notification setting.
_Avoid_: In-app notification

**Push**:
An alert on a Person's phone announcing an Update Notification, delivered through the Mobile App. Opening it leads to what changed.

**Installation**:
One deployed Memento, serving one Immich library to its own People.
_Avoid_: Instance, Site

**Mobile App**:
The viewer-only Memento app for phones. It connects to an Installation and offers viewing and Update Notifications; curation stays on the web.
_Avoid_: Native app, Client

**Onboarding**:
The first-time introduction completed by every Person who gains login access. Completion establishes that Person's notification baseline from everything currently visible to them.
