import { expect, it } from "vitest";

import type { AudienceChange } from "../../types/generated/publishing";
import { audienceSummary } from "./access-labels";

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
