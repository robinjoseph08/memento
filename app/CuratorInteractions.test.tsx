import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { CuratorInteractions } from "./CuratorInteractions";

const session = {
  display_name: "Curator",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: true,
  onboarding_required: false,
};

function json(value: unknown) {
  return Promise.resolve(
    new Response(JSON.stringify(value), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function pathOf(input: RequestInfo | URL) {
  return typeof input === "string"
    ? input
    : input instanceof URL
      ? input.href
      : input.url;
}

function renderInteractions(curator = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <CuratorInteractions session={{ ...session, curator }} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("gives the Curator a discoverable private moderation and Favorite surface", async () => {
  const recipientID = "11111111-1111-4111-8111-111111111111";
  const mediaID = "22222222-2222-4222-8222-222222222222";
  const activeID = "33333333-3333-4333-8333-333333333333";
  const moderatedID = "44444444-4444-4444-8444-444444444444";
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  let activeState = "active";
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      requests.push({ path, init });
      if (path === "/api/comments/curator?limit=50") {
        return json({
          comments: [
            {
              id: activeID,
              media_item_id: mediaID,
              author_person_id: recipientID,
              author_name: "Alex",
              body: activeState === "active" ? "Please keep this private" : "",
              state: activeState,
              version: activeState === "active" ? 1 : 2,
              created_at: "2026-07-28T12:00:00Z",
              edited_at: null,
              moderated_at:
                activeState === "moderated" ? "2026-07-28T12:02:00Z" : null,
              moderator_name: activeState === "moderated" ? "Curator" : null,
              authored_by_me: false,
              can_edit: false,
              can_delete: false,
              can_moderate: activeState === "active",
            },
            {
              id: moderatedID,
              media_item_id: mediaID,
              author_person_id: recipientID,
              author_name: "Alex",
              body: "",
              state: "moderated",
              version: 2,
              created_at: "2026-07-28T11:00:00Z",
              edited_at: null,
              moderated_at: "2026-07-28T11:01:00Z",
              moderator_name: "Curator",
              authored_by_me: false,
              can_edit: false,
              can_delete: false,
              can_moderate: false,
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/people?query=&include_archived=false") {
        return json({
          people: [
            {
              id: recipientID,
              display_name: "Alex",
              sort_name: "alex",
              version: 1,
              status: "active",
              created_at: "2026-07-28T10:00:00Z",
              updated_at: "2026-07-28T10:00:00Z",
              roles: ["recipient"],
              unrevoked_sessions: 1,
              historical_audit_count: 0,
            },
            {
              id: "55555555-5555-4555-8555-555555555555",
              display_name: "Pat",
              sort_name: "pat",
              version: 1,
              status: "active",
              created_at: "2026-07-28T10:00:00Z",
              updated_at: "2026-07-28T10:00:00Z",
              roles: [],
              unrevoked_sessions: 0,
              historical_audit_count: 0,
            },
          ],
        });
      }
      if (
        path === `/api/favorites/curator/recipients/${recipientID}?limit=50`
      ) {
        return json({
          recipient_person_id: recipientID,
          media_item_ids: [mediaID],
          next_cursor: null,
        });
      }
      if (path === `/api/curator/media/${mediaID}`) {
        return json({
          id: mediaID,
          media_type: "image",
          width: 1600,
          height: 900,
          local_date_time: "2026-07-28T10:30:00Z",
          available: true,
          filename: "family-picnic.jpg",
          event_titles: ["Family picnic"],
          thumbnail_url: `/api/curator/media/${mediaID}/thumbnail`,
          preview_url: `/api/curator/media/${mediaID}/preview`,
          video_url: "",
        });
      }
      if (path === `/api/comments/${moderatedID}/moderation-history?limit=50`) {
        return json({
          history: [
            {
              prior_state: "active",
              prior_body: "Original private text",
              reason: "Family privacy",
              actor_name: "Curator",
              created_at: "2026-07-28T11:01:00Z",
            },
          ],
          next_cursor: null,
        });
      }
      if (
        path === `/api/comments/${activeID}/moderate` &&
        init?.method === "POST"
      ) {
        activeState = "moderated";
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderInteractions();
  const interactions = await screen.findByText("Comments and Favorites");
  expect(interactions).toBeVisible();
  fireEvent.click(interactions);
  expect(await screen.findByText("Please keep this private")).toBeVisible();
  expect(screen.queryByRole("option", { name: "Pat" })).not.toBeInTheDocument();
  expect(
    requests.some(({ path }) => path.startsWith("/api/favorites/curator")),
  ).toBe(false);

  const commentMedia = screen.getAllByRole("button", {
    name: "View Media context for Alex's Comment",
  })[0];
  expect(commentMedia.querySelector("img")).toHaveAttribute(
    "src",
    `/api/curator/media/${mediaID}/thumbnail`,
  );
  fireEvent.click(commentMedia);
  const mediaDialog = await screen.findByRole("dialog", {
    name: "family-picnic.jpg",
  });
  expect(mediaDialog).toBeVisible();
  expect(within(mediaDialog).getByText(mediaID)).toBeVisible();
  expect(within(mediaDialog).getByText("1600 × 900")).toBeVisible();
  expect(within(mediaDialog).getByText("Family picnic")).toBeVisible();
  expect(
    within(mediaDialog).getByAltText(
      "Moderation preview for family-picnic.jpg",
    ),
  ).toHaveAttribute("src", `/api/curator/media/${mediaID}/preview`);
  fireEvent.click(
    within(mediaDialog).getByRole("button", { name: "Close Media context" }),
  );

  fireEvent.change(await screen.findByLabelText("Recipient"), {
    target: { value: recipientID },
  });
  const favoriteSection = screen
    .getByRole("heading", { name: "Recipient Favorites" })
    .closest("section");
  expect(favoriteSection).not.toBeNull();
  const favoriteMedia = await within(favoriteSection!).findByRole("button", {
    name: "View Media context for this Favorite",
  });
  expect(favoriteMedia.querySelector("img")).toHaveAttribute(
    "src",
    `/api/curator/media/${mediaID}/thumbnail`,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Show moderation history" }),
  );
  expect(await screen.findByText("Original private text")).toBeVisible();
  expect(screen.getByText(/Family privacy/)).toBeVisible();

  vi.spyOn(window, "prompt").mockReturnValue("Private detail");
  fireEvent.click(screen.getByRole("button", { name: "Moderate Comment" }));
  await waitFor(() =>
    expect(
      requests.find(({ path }) => path === `/api/comments/${activeID}/moderate`)
        ?.init,
    ).toMatchObject({
      method: "POST",
      headers: {
        "If-Match": "1",
        "X-Memento-CSRF": session.csrf_token,
      },
    }),
  );
  expect(
    JSON.parse(
      requests.find(({ path }) => path === `/api/comments/${activeID}/moderate`)
        ?.init?.body as string,
    ),
  ).toEqual({
    reason: "Private detail",
  });
});

test("renders no Curator data surface for a non-Curator session", () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  renderInteractions(false);
  expect(screen.queryByText("Comments and Favorites")).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});
