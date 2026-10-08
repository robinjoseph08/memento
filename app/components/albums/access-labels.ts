import type {
  AccessPerson,
  AudienceChange,
  MomentCircle,
} from "../../types/generated/publishing";
import { countLabel } from "./moment-labels";

// Most-seen people first, then alphabetical, so the busiest rows lead.
export function byPresence(left: AccessPerson, right: AccessPerson) {
  if (left.supporting_entries !== right.supporting_entries)
    return right.supporting_entries - left.supporting_entries;
  return left.display_name.localeCompare(right.display_name);
}

// One line under a name: where the person was seen and whether their access
// comes from the Album or a Join rather than this Moment.
export function accessDetail(person: AccessPerson) {
  const seen = person.detected
    ? `seen in ${countLabel(person.supporting_entries, "item", "items")}`
    : "not seen in this Moment";
  if (person.inherited) return `Album access, ${seen}`;
  if (person.joined_count > 0) return `Joined the Album, ${seen}`;
  if (person.suggested) return "Detected here, not shared yet";
  return seen.charAt(0).toUpperCase() + seen.slice(1);
}

// One line under a Circle's name: its size and how the Album Offer applies
// to this Moment.
export function offerDetail(circle: MomentCircle) {
  const people = countLabel(circle.member_count, "person", "people");
  if (circle.album_offered && circle.decision === "withhold")
    return `Withheld from the Album Offer, ${people}`;
  if (circle.album_offered && !circle.decision) return `Album Offer, ${people}`;
  return people;
}

const items = (ids: string[]) => countLabel(ids.length, "item", "items");

// What one Person gains or loses after a change: media of their own, media
// offered to their Circles, and media moving between the two, which they can
// see either way and so is never described as a loss.
export function audienceSummary(change: AudienceChange) {
  const offeredBefore = new Set(change.offered_lost_entry_ids);
  const offeredAfter = new Set(change.offered_gained_entry_ids);
  const nowOwn = change.gained_entry_ids.filter((id) => offeredBefore.has(id));
  const nowOffered = change.lost_entry_ids.filter((id) => offeredAfter.has(id));
  const moved = new Set([...nowOwn, ...nowOffered]);
  const only = (ids: string[]) => ids.filter((id) => !moved.has(id));
  const gained = only(change.gained_entry_ids);
  const lost = only(change.lost_entry_ids);
  const offered = only(change.offered_gained_entry_ids);
  const withdrawn = only(change.offered_lost_entry_ids);
  return [
    gained.length > 0 && `gains ${items(gained)}`,
    nowOwn.length > 0 && `gets ${items(nowOwn)} they were offered`,
    offered.length > 0 && `is offered ${items(offered)}`,
    nowOffered.length > 0 &&
      `can still browse ${items(nowOffered)} through an Offer`,
    lost.length > 0 && `loses ${items(lost)}`,
    withdrawn.length > 0 && `is no longer offered ${items(withdrawn)}`,
  ]
    .filter(Boolean)
    .join(", ");
}
