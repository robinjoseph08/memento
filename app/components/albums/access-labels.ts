import type { AccessPerson } from "../../types/generated/publishing";
import { countLabel } from "./moment-labels";

// Most-seen people first, then alphabetical, so the busiest rows lead.
export function byPresence(left: AccessPerson, right: AccessPerson) {
  if (left.supporting_entries !== right.supporting_entries)
    return right.supporting_entries - left.supporting_entries;
  return left.display_name.localeCompare(right.display_name);
}

// One line under a name: where the person was seen and whether their access
// comes from the Album rather than this Moment.
export function accessDetail(person: AccessPerson) {
  const seen = person.detected
    ? `seen in ${countLabel(person.supporting_entries, "item", "items")}`
    : "not seen in this Moment";
  if (person.inherited) return `Album access, ${seen}`;
  if (person.suggested) return "Detected here, not shared yet";
  return seen.charAt(0).toUpperCase() + seen.slice(1);
}
