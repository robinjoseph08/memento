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

**Moment**:
A Curator-only portion of an Album whose media was captured around the same time and usually involves the same people. Moments default to capture days but may be split, merged, or span midnight.
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
A Person who can organize Albums, manage access, and invite people. One installation may have multiple Curators.

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

**Unannounced Change**:
An Album or media item that became visible to a Person after that Person's notification baseline. Completing onboarding or successfully sending an Update Notification advances the baseline.

**Update Notification**:
A Curator-approved in-app summary of one or more Unannounced Changes for a Person. Email is an optional delivery channel for the same notification and respects the Person's email preference.

**Onboarding**:
The first-time introduction completed by every Person who gains login access. Completion establishes that Person's notification baseline from everything currently visible to them.
