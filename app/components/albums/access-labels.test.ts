import { expect, it } from "vitest";

import type {
  AccessPerson,
  AudienceChange,
  MomentCircle,
} from "../../types/generated/publishing";
import { accessDetail, audienceSummary, offerDetail } from "./access-labels";

function change(lists: Partial<AudienceChange>): AudienceChange {
  return {
    person_id: "sam",
    display_name: "Sam",
    gained_entry_ids: [],
    lost_entry_ids: [],
    offered_gained_entry_ids: [],
    offered_lost_entry_ids: [],
    ...lists,
  };
}

it("counts each change in items", () => {
  expect(audienceSummary(change({ gained_entry_ids: ["a"] }))).toBe(
    "gains 1 item",
  );
  expect(
    audienceSummary(
      change({ lost_entry_ids: ["a", "b"], offered_gained_entry_ids: ["c"] }),
    ),
  ).toBe("is offered 1 item, loses 2 items");
  expect(audienceSummary(change({ offered_lost_entry_ids: ["a", "b"] }))).toBe(
    "is no longer offered 2 items",
  );
});

it("describes media moving between an Offer and a Person's own access as a move", () => {
  expect(
    audienceSummary(
      change({
        gained_entry_ids: ["a", "b", "c"],
        offered_lost_entry_ids: ["a", "b"],
      }),
    ),
  ).toBe("gains 1 item, gets 2 items they were offered");
  expect(
    audienceSummary(
      change({
        lost_entry_ids: ["a", "b"],
        offered_gained_entry_ids: ["a", "b"],
      }),
    ),
  ).toBe("can still browse 2 items through an Offer");
  expect(
    audienceSummary(
      change({ lost_entry_ids: ["a", "b"], offered_gained_entry_ids: ["b"] }),
    ),
  ).toBe("can still browse 1 item through an Offer, loses 1 item");
});

it("says how an Album Offer applies to a Moment's Circle", () => {
  const circle = (fields: Partial<MomentCircle>): MomentCircle => ({
    circle_id: "extended",
    name: "Extended family",
    member_count: 3,
    decision: "",
    album_offered: false,
    offered: false,
    ...fields,
  });
  expect(offerDetail(circle({ album_offered: true, offered: true }))).toBe(
    "Album Offer, 3 people",
  );
  expect(
    offerDetail(circle({ album_offered: true, decision: "withhold" })),
  ).toBe("Withheld from the Album Offer, 3 people");
  expect(offerDetail(circle({ decision: "offer", member_count: 1 }))).toBe(
    "1 person",
  );
});

it("says when a Person sees a Moment because they joined the Album", () => {
  const person: AccessPerson = {
    person_id: "sam",
    display_name: "Sam",
    avatar_url: "",
    decision: "",
    detected: true,
    suggested: false,
    supporting_entries: 2,
    inherited: false,
    effective: false,
    accessible_count: 0,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
    offering_circles: ["Extended family"],
    joined_count: 3,
    joined: true,
  };
  expect(accessDetail(person)).toBe("Joined the Album, seen in 2 items");
  expect(accessDetail({ ...person, joined_count: 0 })).toBe("Seen in 2 items");
});
