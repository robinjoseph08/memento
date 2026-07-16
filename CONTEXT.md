# Family Photo Sharing

This context covers the private publication of one curator's photos and videos to selected family members.

## Language

**Curator**:
The sole person who publishes media and controls who may access it.
_Avoid_: Publisher, admin, photographer

**Recipient**:
An invited family member who may receive access to published media but does not publish or manage sharing.
_Avoid_: User, viewer, contributor

**Person**:
A family member who may attend a moment, whether or not they have a recipient account.
_Avoid_: Contact, profile, face

**Attendance**:
The curator-confirmed people who were present at a moment; face detections may suggest people but are not authoritative.
_Avoid_: Detected faces, appearances

**Interest list**:
The people whose attendance causes a recipient to be suggested for a moment; it influences an audience proposal but never grants access.
_Avoid_: Permissions, subscriptions, access list

**Family branch**:
A person's partner, every descendant, and every descendant's partner recursively through all generations; it seeds that recipient's interest list.
_Avoid_: Immediate family, household

**Event**:
A narrative container for related photos and videos, which may contain moments with different audiences.
_Avoid_: Album, gallery

**Moment**:
A curator-only part of an event with one audience, often used where attendance changes; recipients see one filtered event rather than its moment boundaries.
_Avoid_: Sub-album, segment

**Loose item**:
A photo or video shared independently rather than through an event.
_Avoid_: One-off, loose photo

**Audience proposal**:
The system's suggested recipients for a moment, based on attendance, interest lists, or curator choices, which becomes an audience only after curator approval.
_Avoid_: Automatic sharing, recipient list

**Audience**:
The approved recipients allowed to access a moment or loose item.
_Avoid_: Members, invitees

**Publication**:
The curator's approval that makes an event or subsequent update visible to its audiences.
_Avoid_: Sync, import

**Staged update**:
The single coalescing set of source-library changes to a published event that remains private until the curator publishes it.
_Avoid_: Live sync, pending upload

**Notification preference**:
A recipient's choice to receive publication emails immediately, in a weekly digest, or not at all.
_Avoid_: Access preference, subscription

**Favorite**:
A recipient's personal selection of a photo or video, visible to that recipient and the curator but hidden from other recipients.
_Avoid_: Like, reaction, public favorite

**Comment**:
A message on a photo or video, visible only to the curator and recipients who can access that item.
_Avoid_: Event comment, public comment
