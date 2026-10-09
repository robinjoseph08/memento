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

**Linked Email**:
A verified email address a Person can sign in with. A Person may have several, and Google or a Sign-in Code are the two ways to verify one.
_Avoid_: Identity, Account, Linked account

**Sign-in Code**:
A short-lived code emailed to an address so its owner can prove they control it. Verifying one signs in a known Person or starts an Access Request for an unknown address.
_Avoid_: OTP, Token, Magic Link, Passcode

**Preauthorization**:
A Curator's approval for a specific email address to join Memento once its owner verifies that address.
_Avoid_: Allowlist

**Invitation**:
A proactive email that introduces a preauthorized Person to Memento and starts the standard onboarding flow.
_Avoid_: Preauthorization

**Access Request**:
A verified request either to join Memento or to view an inaccessible Album. It records the requester and attempted Album when known but grants no access by itself.

**Access Recommendation**:
A suggestion to share a Moment with a Person detected in its current media. Recommendations follow Moment membership and never change access without a Curator's approval.

**Access Decision**:
A Curator's explicit access choice for a Person. Albums may allow access, Moments and Album Entries may allow or deny it, and missing decisions inherit from broader scopes. When no decision applies, Offers to that Person's Circles decide before access defaults to denied.

**Circle**:
A Curator-named set of People, such as extended family or college friends, that Albums can be made available to. A Person may belong to several Circles. Circle names are only ever shown to Curators.
_Avoid_: Group, Audience, Viewing Group

**Offer**:
A Curator's choice to make an Album or one of its Moments available to a Circle, so its members can browse it apart from their own Albums and Join it without asking. An Album Offer covers its future Moments unless a Moment is withheld, and Offers apply only while the Album is published. Membership is live: a Person added to a Circle can see everything already offered to it, and loses it when removed. Any Access Decision for a specific Person outranks an Offer.
_Avoid_: Share, Publish, Grant

**Join**:
A viewer's choice to include everything offered to them in one Album alongside whatever they were granted directly, placing the Album among their own. Leave reverses it. A Join outlasts the Offers it covers, so an Album offered again returns to the People who joined it. Offered media a Person has not joined stays out of their own Albums and Library but remains browsable apart from them.
_Avoid_: Follow, Subscribe, Add

**Cover Order**:
A Curator's ordered list of preferred Moments for one Album's cover. Each viewer sees the cover of the first Moment in the Cover Order they can access; when none applies, the earliest accessible Moment cover in capture order is used, so an Album with an empty Cover Order behaves as if none existed.
_Avoid_: Priority, Ranking, Pin, Cover Override

**Viewing Group**:
The People who can see exactly the same Moment covers in one Album. Viewing Groups are derived from Access Decisions, Offers, and Joins whenever a Curator reviews an Album's cover; they are never stored, named, or shown to viewers. A Person who can see no Moment cover belongs to no Viewing Group.
_Avoid_: Cohort, Access Set, Segment

**Unannounced Change**:
An Album or Album Entry that became visible to a Person after that Person's notification baseline, counting only media they were granted directly or joined. An Album also counts once, as new to view, the first time it holds offered media that Person has not joined, unless that Person has turned off hearing about Albums they can join, in which case it is announced silently. Completing Onboarding, approving an Update Notification, or dismissing selected changes advances the baseline, independently of email or Push delivery. Joining an Album advances it for the media joined, so only media added after the Join is new.

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

**Hand-off Code**:
A single-use code the web sign-in gives the Mobile App so the phone gets a session of its own. It verifies nothing and only moves an already signed-in Person onto their phone, unlike a Sign-in Code.
_Avoid_: Mobile sign-in code, Exchange code

**Onboarding**:
The first-time introduction completed by every Person who gains login access. Completion establishes that Person's notification baseline from everything currently visible or offered to them.
