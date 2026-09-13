import { focusManager } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";
import type { Album, AlbumDetail } from "../../types/generated/publishing";

const album: Album = {
  id: "album-1",
  source_id: "summer",
  title: "Summer by the sea",
  description: "A week away",
  published: false,
  status: "complete",
  message: "",
  processed: 36,
  total: 36,
  photo_count: 35,
  video_count: 1,
  start_date: "2026-07-01",
  end_date: "2026-07-07",
  cover_url: "/media/beach",
};

const person = { id: "robin", display_name: "Robin", is_curator: true };
const source = {
  id: "summer",
  title: "Summer by the sea",
  description: "A week away",
  count: 36,
  start_date: "2026-07-01T12:00:00Z",
  end_date: "2026-07-07T12:00:00Z",
  cover_url: "/api/curator/sources/summer/cover",
  album_id: "",
};

// Wide viewports show the outline beside the selected Moment. Narrow ones show
// the outline first and drill into the Moment pane.
function desktopViewport() {
  vi.stubGlobal("matchMedia", (media: string) => ({
    media,
    matches: /min-width/.test(media),
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

function mockAPI(
  handler: (
    path: string,
    options?: RequestInit,
  ) => Response | Promise<Response>,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Promise.resolve(
          Response.json({ claimed: true, person, auth_mode: "fake" }),
        );
      if (path.endsWith("/connection"))
        return Promise.resolve(
          Response.json({
            usable: true,
            import_supported: true,
            version: "2.7.5",
            message: "",
          }),
        );
      return Promise.resolve(handler(path, options));
    }),
  );
}

it("opens the library from an empty collection and links previously imported sources", async () => {
  mockAPI((path) =>
    path.endsWith("/albums")
      ? Response.json([])
      : Response.json({
          albums: [{ ...source, album_id: album.id }],
          page: 1,
          pages: 1,
          total: 1,
        }),
  );
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  await user.click(screen.getByRole("link", { name: "Import an album" }));
  expect(
    await screen.findByRole("link", { name: "Open album" }),
  ).toHaveAttribute("href", "/curator/albums/album-1");
  expect(
    screen.queryByRole("button", { name: "Import" }),
  ).not.toBeInTheDocument();
});

it("imports an album, shows progress, then reveals unpublished Moments with compact media", async () => {
  desktopViewport();
  let imported = false;
  let completed = false;
  mockAPI((path, options) => {
    if (path.includes("/sources?"))
      return Response.json({ albums: [source], page: 1, pages: 1, total: 1 });
    if (path.endsWith("/imports")) {
      expect(JSON.parse(String(options?.body))).toEqual({
        source_id: "summer",
      });
      imported = true;
      return Response.json({
        ...album,
        status: "queued",
        processed: 0,
        moments: [],
      });
    }
    return Response.json(
      completed
        ? completeAlbum
        : { ...album, status: "processing", processed: 12, moments: [] },
    );
  });
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: "Import" }));
  expect(imported).toBe(true);
  await waitFor(() =>
    expect(
      screen.getByRole("progressbar", { name: "Import progress" }),
    ).toHaveAttribute("value", "12"),
  );
  expect(
    screen.queryByRole("textbox", { name: "Album title" }),
  ).not.toBeInTheDocument();
  completed = true;
  expect(
    await screen.findByRole(
      "heading",
      { name: "First day" },
      { timeout: 4000 },
    ),
  ).toBeVisible();
  expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  expect(screen.getByText("Unpublished")).toBeVisible();
  const moment = screen.getByRole("region", { name: "First day" });
  expect(
    within(moment).getByRole("list", { name: "Moment media" }),
  ).toBeVisible();
  expect(within(moment).getByText("1 photo, 1 video")).toBeVisible();
  expect(screen.getByRole("img", { name: "Beach.jpg" })).toHaveAttribute(
    "src",
    "/media/beach",
  );
  expect(document.title).toBe("Summer by the sea | Memento");
});

const completeAlbum: AlbumDetail = {
  ...album,
  photo_count: 1,
  video_count: 1,
  access: [],
  moments: [
    {
      id: "day-1",
      title: "First day",
      label: "First day",
      date: "2026-07-01",
      end_date: "2026-07-01",
      cover_entry_id: "photo",
      access: { people: [], faces: [] },
      entries: [
        {
          id: "photo",
          decisions: {},
          media_id: "m-photo",
          filename: "Beach.jpg",
          kind: "IMAGE",
          captured_at: "2026-07-01T12:00:00",
          available: true,
          thumbnail_url: "/media/beach",
          title: "",
          chapters: [],
          chapter_status: "",
          chapter_message: "",
          playback_url: "",
        },
        {
          id: "video",
          decisions: {},
          media_id: "m-video",
          filename: "Waves.mp4",
          kind: "VIDEO",
          captured_at: "2026-07-01T13:00:00",
          available: true,
          thumbnail_url: "/media/waves",
          title: "",
          chapters: [],
          chapter_status: "",
          chapter_message: "",
          playback_url: "",
        },
      ],
    },
  ],
};

it("identifies the Album in its header with counts and publication state", async () => {
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  render(<App />);
  const heading = await screen.findByRole("heading", {
    name: "Summer by the sea",
    level: 1,
  });
  const header = within(heading.closest("header")!);
  expect(header.getByText("1 photo, 1 video")).toBeVisible();
  expect(header.getByText("Unpublished")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: "Album cover" }),
  ).not.toBeInTheDocument();
});

it("keeps an absent Immich description empty and shows a read-only placeholder", async () => {
  desktopViewport();
  mockAPI(() => Response.json({ ...completeAlbum, description: "" }));
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=details",
  );
  render(<App />);
  const description = await screen.findByRole("textbox", {
    name: "Description from Immich",
  });
  expect(description).toHaveValue("");
  expect(description).toHaveAttribute(
    "placeholder",
    "No description in Immich.",
  );
  expect(description).toHaveAttribute("readonly");
});

it("shows weekdays and local capture times without visible filenames, with a video badge", async () => {
  desktopViewport();
  mockAPI(() =>
    Response.json({
      ...completeAlbum,
      moments: [
        {
          ...completeAlbum.moments[0],
          label: "July 1, 2026",
          entries: completeAlbum.moments[0].entries.map((entry) => ({
            ...entry,
            captured_at:
              entry.kind === "IMAGE"
                ? "2026-07-01T00:30:00"
                : "2026-07-01T23:59:00",
          })),
        },
      ],
    }),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Wednesday, July 1, 2026" }),
  ).toBeVisible();
  expect(screen.getByText("12:30 AM")).toHaveAttribute(
    "datetime",
    "2026-07-01T00:30:00",
  );
  expect(screen.getByText("11:59 PM")).toBeVisible();
  expect(screen.queryByText("Beach.jpg")).not.toBeInTheDocument();
  expect(screen.queryByText("Waves.mp4")).not.toBeInTheDocument();
  expect(screen.getByRole("img", { name: "Video" })).toHaveAttribute(
    "title",
    "Video",
  );
});

it("keeps the outline beside the selected Moment and preserves title edits between sections", async () => {
  desktopViewport();
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const outline = await screen.findByRole("navigation", {
    name: "Album outline",
  });
  const momentRow = within(outline).getByRole("link", { name: /First day/ });
  expect(momentRow).toHaveAttribute("aria-current", "page");
  expect(within(outline).getByText("No access yet")).toBeVisible();
  expect(screen.getByRole("region", { name: "First day" })).toBeVisible();
  expect(screen.getByRole("img", { name: "Beach.jpg" })).toBeVisible();
  const details = within(outline).getByRole("link", { name: "Album details" });
  await user.click(details);
  expect(details).toHaveAttribute("aria-current", "page");
  expect(momentRow).not.toHaveAttribute("aria-current");
  expect(
    screen.queryByRole("region", { name: "First day" }),
  ).not.toBeInTheDocument();
  const title = screen.getByRole("textbox", { name: "Album title" });
  await user.clear(title);
  await user.type(title, "Unsaved weekend");
  await user.click(momentRow);
  expect(
    screen.queryByRole("textbox", { name: "Album title" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("region", { name: "First day" })).toBeVisible();
  await user.click(details);
  expect(screen.getByRole("textbox", { name: "Album title" })).toHaveValue(
    "Unsaved weekend",
  );
});

it("drills from the outline into a Moment and back on narrow screens", async () => {
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const outline = await screen.findByRole("navigation", {
    name: "Album outline",
  });
  expect(
    screen.queryByRole("region", { name: "First day" }),
  ).not.toBeInTheDocument();
  await user.click(within(outline).getByRole("link", { name: /First day/ }));
  expect(
    screen.queryByRole("navigation", { name: "Album outline" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("region", { name: "First day" })).toBeVisible();
  expect(screen.getByRole("img", { name: "Beach.jpg" })).toBeVisible();
  await user.click(screen.getByRole("link", { name: "Outline" }));
  expect(
    screen.getByRole("navigation", { name: "Album outline" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("region", { name: "First day" }),
  ).not.toBeInTheDocument();
});

it("uses the Outline pane for selection and saves Moment access explicitly", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "allow",
    detected: true,
    suggested: false,
    supporting_entries: 1,
    inherited: false,
    effective: true,
    accessible_count: 2,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  const sam = {
    person_id: "sam",
    display_name: "Sam",
    avatar_url: "",
    decision: "",
    detected: true,
    suggested: true,
    supporting_entries: 1,
    inherited: false,
    effective: false,
    accessible_count: 0,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  let current: AlbumDetail = {
    ...completeAlbum,
    moments: [
      {
        ...completeAlbum.moments[0],
        access: { people: [alex, sam], faces: [] },
      },
      {
        id: "day-2",
        title: "",
        label: "July 2, 2026",
        date: "2026-07-02",
        end_date: "2026-07-02",
        cover_entry_id: "second-photo",
        entries: [
          {
            ...completeAlbum.moments[0].entries[0],
            id: "second-photo",
            media_id: "second-media",
            filename: "Cliffs.jpg",
          },
        ],
        access: { people: [], faces: [] },
      },
    ],
  };
  const requests: Array<{ path: string; body: unknown }> = [];
  mockAPI((path, options) => {
    if (options?.method !== "POST") return Response.json(current);
    const body: unknown = JSON.parse(String(options.body));
    requests.push({ path, body });
    if (path.endsWith("/rules")) {
      current = {
        ...current,
        moments: current.moments.map((moment) =>
          moment.id === "day-1"
            ? {
                ...moment,
                access: {
                  ...moment.access,
                  people: moment.access.people.map((person) =>
                    person.person_id === "sam"
                      ? {
                          ...person,
                          decision: "allow",
                          effective: true,
                          accessible_count: 2,
                          suggested: false,
                        }
                      : person,
                  ),
                },
              }
            : moment,
        ),
      };
      return Response.json(current);
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=moments&moment=day-1",
  );
  const user = userEvent.setup();
  render(<App />);

  const inspector = await screen.findByRole("region", {
    name: "Moment access",
  });
  expect(within(inspector).getByText("Allowed")).toBeVisible();
  expect(
    within(inspector).getByRole("heading", { name: "Suggested 1" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("checkbox", { name: "Select Waves.mp4" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Move" }),
  ).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Select" }));
  expect(screen.getByRole("button", { name: "Move" })).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: "Select Waves.mp4" }));
  expect(screen.getByRole("button", { name: "Move" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Split" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Set as cover" })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Done" }));
  expect(
    screen.queryByRole("checkbox", { name: "Select Waves.mp4" }),
  ).not.toBeInTheDocument();

  const saveAccess = within(inspector).getByRole("button", {
    name: "Save Moment access",
  });
  expect(saveAccess).toBeDisabled();
  await user.click(
    within(inspector).getByRole("button", { name: "Add all suggested" }),
  );
  expect(
    within(inspector).getByRole("checkbox", {
      name: "Allow Sam for this Moment",
    }),
  ).toBeChecked();
  expect(requests.filter((request) => request.path.endsWith("/rules"))).toEqual(
    [],
  );
  await user.click(saveAccess);
  await waitFor(() =>
    expect(requests.at(-1)).toEqual({
      path: "/api/curator/albums/album-1/moments/day-1/rules",
      body: { decisions: [{ person_id: "sam", decision: "allow" }] },
    }),
  );
  expect(
    await within(inspector).findByText("Moment access saved."),
  ).toBeVisible();
  expect(
    within(inspector).getByRole("heading", { name: "Allowed 2" }),
  ).toBeVisible();
});

it.each([
  { broaderAllowed: false, decision: "allow", expected: "inherit" },
  { broaderAllowed: true, decision: "", expected: "deny" },
  { broaderAllowed: true, decision: "allow", expected: "deny" },
  { broaderAllowed: true, decision: "deny", expected: "inherit" },
])(
  "toggles Moment access with $expected for $decision when broader access is $broaderAllowed",
  async ({ broaderAllowed, decision, expected }) => {
    const effective = decision !== "deny";
    desktopViewport();
    let current = {
      ...completeAlbum,
      moments: [
        {
          ...completeAlbum.moments[0],
          access: {
            people: [
              {
                person_id: "alex",
                display_name: "Alex",
                avatar_url: "",
                decision,
                inherited: broaderAllowed,
                effective,
                detected: false,
                suggested: false,
                supporting_entries: 0,
                accessible_count: effective ? 1 : 0,
                exceptions: 1,
                moments_detected: 0,
                deactivated: false,
              },
            ],
            faces: [],
          },
        },
      ],
    };
    current.access = [
      {
        ...current.moments[0].access.people[0],
        decision: broaderAllowed ? "allow" : "",
        inherited: false,
      },
    ];
    let saved: unknown;
    mockAPI((path, options) => {
      if (path.endsWith("/rules")) {
        saved = JSON.parse(String(options?.body));
        current = {
          ...current,
          moments: [
            {
              ...current.moments[0],
              access: {
                people: [
                  {
                    ...current.moments[0].access.people[0],
                    decision: expected === "deny" ? "deny" : "",
                    effective: !effective,
                    accessible_count: effective ? 0 : 2,
                  },
                ],
                faces: [],
              },
            },
          ],
        };
        return Response.json(current);
      }
      return Response.json(current);
    });
    window.history.replaceState(null, "", "/curator/albums/album-1");
    const user = userEvent.setup();
    render(<App />);
    const checkbox = await screen.findByRole("checkbox", {
      name: "Allow Alex for this Moment",
    });
    if (effective) expect(checkbox).toBeChecked();
    else expect(checkbox).not.toBeChecked();
    if (effective)
      expect(
        within(
          screen.getByRole("navigation", { name: "Album outline" }),
        ).getByRole("img", { name: "Allowed: Alex" }),
      ).toBeVisible();
    expect(
      screen.getByText(
        broaderAllowed
          ? "Album access, not seen in this Moment"
          : "Not seen in this Moment",
      ),
    ).toBeVisible();
    await user.click(checkbox);
    expect(saved).toBeUndefined();
    await user.click(
      screen.getByRole("button", { name: "Save Moment access" }),
    );
    await waitFor(() =>
      expect(saved).toEqual({
        decisions: [{ person_id: "alex", decision: expected }],
      }),
    );
    const toggled = screen.getByRole("checkbox", {
      name: "Allow Alex for this Moment",
    });
    if (effective) expect(toggled).not.toBeChecked();
    else expect(toggled).toBeChecked();
  },
);

it("asks before leaving Album access with an unsaved draft", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "",
    inherited: false,
    effective: false,
    detected: true,
    suggested: false,
    supporting_entries: 1,
    moments_detected: 1,
    deactivated: false,
    accessible_count: 0,
    exceptions: 0,
  };
  mockAPI((path) =>
    path.endsWith("/access/preview")
      ? Response.json({ changes: [] })
      : Response.json({ ...completeAlbum, access: [alex] }),
  );
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=access&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("checkbox", { name: "Album access for Alex" }),
  );
  fireEvent.click(screen.getByRole("link", { name: "Album details" }));
  const leave = await screen.findByRole("dialog", { name: "Leave this page?" });
  await user.click(within(leave).getByRole("button", { name: "Cancel" }));
  expect(
    screen.getByRole("checkbox", { name: "Album access for Alex" }),
  ).toBeChecked();
  expect(new URLSearchParams(window.location.search).get("section")).toBe(
    "access",
  );
});

it("reviews visibility before saving Album access and removes only the Album allow", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "allow",
    inherited: false,
    effective: true,
    detected: true,
    suggested: false,
    supporting_entries: 2,
    accessible_count: 1,
    exceptions: 1,
    moments_detected: 1,
    deactivated: false,
  };
  const sam = {
    ...alex,
    person_id: "sam",
    display_name: "Sam",
    decision: "",
    effective: false,
    detected: false,
    supporting_entries: 0,
    accessible_count: 0,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  let current = { ...completeAlbum, access: [alex, sam] };
  const posts: Array<{ path: string; body: unknown }> = [];
  mockAPI((path, options) => {
    if (path.endsWith("/access/preview")) {
      posts.push({ path, body: JSON.parse(String(options?.body)) });
      return Response.json({
        changes: [
          {
            person_id: "alex",
            display_name: "Alex",
            gained_entry_ids: [],
            lost_entry_ids: ["photo"],
          },
        ],
      });
    }
    if (path.endsWith("/access")) {
      posts.push({ path, body: JSON.parse(String(options?.body)) });
      current = {
        ...current,
        access: [{ ...alex, decision: "", accessible_count: 0 }],
      };
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=access&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  const checkbox = await screen.findByRole("checkbox", {
    name: "Album access for Alex",
  });
  expect(checkbox).toBeChecked();
  const everywhere = screen.getByRole("region", { name: "In every Moment 1" });
  expect(within(everywhere).getByText("Seen in 1 of 1 Moment")).toBeVisible();
  expect(
    within(everywhere).getByText("1 of 2 items accessible now, 1 exception"),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Allow everyone in every Moment" }),
  ).not.toBeInTheDocument();
  const samAccess = screen.getByRole("checkbox", {
    name: "Album access for Sam",
  });
  expect(samAccess).not.toBeVisible();
  await user.click(screen.getByText("1 person not seen in this album"));
  expect(samAccess).toBeVisible();
  expect(samAccess).not.toBeChecked();
  const save = screen.getByRole("button", { name: "Save Album access" });
  expect(save).toBeDisabled();
  expect(screen.getByText("Change access above to review it.")).toBeVisible();
  await user.click(checkbox);
  expect(checkbox).not.toBeChecked();
  await user.click(
    screen.getByRole("button", { name: "Allow everyone in every Moment" }),
  );
  expect(checkbox).toBeChecked();
  expect(screen.getByText("Change access above to review it.")).toBeVisible();
  await user.click(checkbox);
  expect(checkbox).not.toBeChecked();
  const review = screen.getByRole("region", { name: "Visibility review" });
  expect(await within(review).findByText("Alex")).toBeVisible();
  expect(within(review).getByText("loses 1")).toBeVisible();
  expect(posts.at(-1)).toEqual({
    path: "/api/curator/albums/album-1/access/preview",
    body: { people: [{ person_id: "alex", allowed: false }] },
  });
  await user.click(save);
  await waitFor(() =>
    expect(posts.at(-1)).toEqual({
      path: "/api/curator/albums/album-1/access",
      body: { people: [{ person_id: "alex", allowed: false }] },
    }),
  );
  expect(await screen.findByText("Album access saved.")).toBeVisible();
  expect(
    screen.getByText("0 of 2 items accessible now, 1 exception"),
  ).toBeVisible();
  expect(checkbox).not.toBeChecked();
});

it("keeps a Moment access draft after a failed save and shows the error", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "allow",
    inherited: false,
    effective: true,
    detected: false,
    suggested: false,
    supporting_entries: 0,
    moments_detected: 0,
    deactivated: false,
    accessible_count: 2,
    exceptions: 0,
  };
  const current = {
    ...completeAlbum,
    access: [alex],
    moments: [
      { ...completeAlbum.moments[0], access: { people: [alex], faces: [] } },
    ],
  };
  let resolveSave: ((response: Response) => void) | undefined;
  mockAPI((path) =>
    path.endsWith("/rules")
      ? new Promise<Response>((resolve) => {
          resolveSave = resolve;
        })
      : Response.json(current),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const checkbox = await screen.findByRole("checkbox", {
    name: "Allow Alex for this Moment",
  });
  await user.click(checkbox);
  await user.click(screen.getByRole("button", { name: "Save Moment access" }));
  expect(checkbox).toBeDisabled();
  expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
  await act(async () =>
    resolveSave?.(
      Response.json(
        { error: { fields: { decisions: "Choose an active Person." } } },
        { status: 400 },
      ),
    ),
  );
  expect(await screen.findByText("Choose an active Person.")).toBeVisible();
  expect(checkbox).not.toBeChecked();
  expect(checkbox).toBeEnabled();
  expect(
    screen.getByRole("button", { name: "Save Moment access" }),
  ).toBeEnabled();
});

it("reviews publication counts and warnings before explicitly publishing without notifications", async () => {
  desktopViewport();
  let current = completeAlbum;
  let published: unknown;
  mockAPI((path, options) => {
    if (path.endsWith("/publication"))
      return Response.json({
        title: current.title,
        photo_count: 1,
        video_count: 1,
        moment_count: 1,
        audience: [
          {
            person_id: "alex",
            display_name: "Alex",
            avatar_url: "",
            accessible_count: 1,
          },
        ],
        blockers: [],
        warnings: ["1 unlinked face. Optional to link."],
        review_token: "reviewed-publication",
      });
    if (path.endsWith("/publish")) {
      published = JSON.parse(String(options?.body));
      current = { ...current, published: true };
    }
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Review & publish" }),
  );
  const review = await screen.findByRole("dialog", {
    name: "Ready to publish?",
  });
  expect(await within(review).findByText("Alex")).toBeVisible();
  expect(within(review).getByText("1 of 2 items")).toBeVisible();
  expect(within(review).getByText("2 items in 1 Moment")).toBeVisible();
  expect(
    within(review).getByText("1 unlinked face. Optional to link."),
  ).toBeVisible();
  expect(within(review).getByText(/sends no notifications/)).toBeVisible();
  expect(published).toBeUndefined();
  await user.click(
    within(review).getByRole("button", { name: "Publish album" }),
  );
  await waitFor(() =>
    expect(published).toEqual({ review_token: "reviewed-publication" }),
  );
  expect(await screen.findByText("Published")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Review & publish" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("displays server blockers as alerts and keeps publishing disabled", async () => {
  desktopViewport();
  mockAPI((path) =>
    path.endsWith("/publication")
      ? Response.json({
          title: completeAlbum.title,
          photo_count: 1,
          video_count: 1,
          moment_count: 1,
          audience: [],
          warnings: [],
          blockers: ["Assign every item to a Moment."],
          review_token: "blocked",
        })
      : Response.json(completeAlbum),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Review & publish" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Ready to publish?",
  });
  expect(
    await within(dialog).findByRole("alert", {
      name: "",
    }),
  ).toHaveTextContent("Assign every item to a Moment.");
  expect(within(dialog).getByText(/No one has access yet/)).toBeVisible();
  expect(
    within(dialog).getByRole("button", { name: "Publish album" }),
  ).toBeDisabled();
  await user.click(
    within(dialog).getByRole("button", { name: "Keep editing" }),
  );
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("unpublishes from the Danger zone without losing curation", async () => {
  desktopViewport();
  let current = { ...completeAlbum, published: true };
  mockAPI((path) => {
    if (path.endsWith("/unpublish")) current = { ...current, published: false };
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=details&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  const danger = await screen.findByRole("region", { name: "Danger zone" });
  expect(
    screen.queryByRole("button", { name: "Review & publish" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Before you publish" }),
  ).not.toBeInTheDocument();
  await user.click(
    within(danger).getByRole("button", { name: "Unpublish Album" }),
  );
  const confirm = screen.getByRole("dialog", { name: "Unpublish Album?" });
  expect(within(confirm).getByText(/Moments, access decisions/)).toBeVisible();
  await user.click(
    within(confirm).getByRole("button", { name: "Unpublish Album" }),
  );
  expect(await screen.findByText("Unpublished")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Review & publish" }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Before you publish" }),
  ).toBeVisible();
  expect(screen.getByText("Album has a title")).toBeVisible();
  expect(screen.getByText("2 items assigned to 1 Moment")).toBeVisible();
  expect(
    within(danger).queryByRole("button", { name: "Unpublish Album" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: /First day/ })).toBeVisible();
});

it("requires the exact Album title for permanent deletion and preserves confirmation after failure", async () => {
  desktopViewport();
  let fail = true;
  let deleted: unknown;
  mockAPI((path, options) => {
    if (path.endsWith("/delete")) {
      deleted = JSON.parse(String(options?.body));
      return fail
        ? Response.json(
            { error: { fields: { title: "Type the Album title exactly." } } },
            { status: 400 },
          )
        : Response.json({});
    }
    if (path === "/api/curator/albums") return Response.json([]);
    return Response.json(completeAlbum);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=details&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: "Delete Album" }));
  const dialog = screen.getByRole("dialog", {
    name: "Permanently delete this Album?",
  });
  expect(within(dialog).getByText(/Immich stays untouched/)).toBeVisible();
  expect(within(dialog).getByText(/other Albums keep/)).toBeVisible();
  const confirm = within(dialog).getByRole("button", {
    name: "Permanently delete Album",
  });
  expect(confirm).toBeDisabled();
  const input = within(dialog).getByRole("textbox", {
    name: "Type the Album title",
  });
  await user.type(input, completeAlbum.title);
  await user.click(confirm);
  expect(
    await screen.findByText("Type the Album title exactly."),
  ).toBeVisible();
  expect(input).toHaveValue(completeAlbum.title);
  expect(input).toHaveFocus();
  fail = false;
  await user.click(confirm);
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  expect(deleted).toEqual({ title: completeAlbum.title });
  expect(window.location.pathname).toBe("/curator");
});

it("reviews every scope before confirming removal of all a person's access", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "allow",
    inherited: false,
    effective: true,
    detected: false,
    suggested: false,
    supporting_entries: 0,
    accessible_count: 1,
    exceptions: 1,
    moments_detected: 1,
    deactivated: false,
  };
  let current = { ...completeAlbum, access: [alex] };
  let removed: unknown;
  mockAPI((path, options) => {
    if (path.endsWith("/remove-all/preview"))
      return Response.json({
        person_id: "alex",
        display_name: "Alex",
        changes: [
          {
            person_id: "alex",
            display_name: "Alex",
            gained_entry_ids: [],
            lost_entry_ids: ["photo"],
          },
        ],
        review_token: "reviewed-removal",
      });
    if (path.endsWith("/remove-all")) {
      removed = JSON.parse(String(options?.body));
      current = {
        ...current,
        access: [
          {
            ...alex,
            decision: "",
            effective: false,
            accessible_count: 0,
            exceptions: 0,
            moments_detected: 1,
            deactivated: false,
          },
        ],
      };
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=access&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Remove all access…" }),
  );
  const review = await screen.findByRole("dialog", {
    name: "Remove all access for Alex?",
  });
  expect(
    within(review).getByText(/Every Album, Moment, and item decision/),
  ).toBeVisible();
  const visibility = within(review).getByRole("region", {
    name: "Visibility review",
  });
  expect(await within(visibility).findByText("Alex")).toBeVisible();
  expect(within(visibility).getByText("loses 1")).toBeVisible();
  expect(removed).toBeUndefined();
  await user.click(
    within(review).getByRole("button", { name: "Remove all access" }),
  );
  await waitFor(() =>
    expect(removed).toEqual({
      person_id: "alex",
      review_token: "reviewed-removal",
    }),
  );
  expect(await screen.findByText("0 of 2 items accessible now")).toBeVisible();
});

it("lists a deactivated person's frozen rules and removes them without offering access", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "allow",
    inherited: false,
    effective: true,
    detected: false,
    suggested: false,
    supporting_entries: 0,
    moments_detected: 0,
    accessible_count: 1,
    exceptions: 1,
    deactivated: true,
  };
  let current = { ...completeAlbum, access: [alex] };
  let removed: unknown;
  mockAPI((path, options) => {
    if (path.endsWith("/remove-all/preview"))
      return Response.json({
        person_id: "alex",
        display_name: "Alex",
        changes: [
          {
            person_id: "alex",
            display_name: "Alex",
            gained_entry_ids: [],
            lost_entry_ids: ["photo"],
          },
        ],
        review_token: "reviewed-removal",
      });
    if (path.endsWith("/remove-all")) {
      removed = JSON.parse(String(options?.body));
      current = { ...current, access: [] };
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=access&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByText("No active viewers to grant access to."),
  ).toBeVisible();
  expect(
    screen.queryByRole("checkbox", { name: "Album access for Alex" }),
  ).not.toBeInTheDocument();
  await user.click(
    screen.getByText("1 deactivated person still has rules here"),
  );
  expect(
    screen.getByText("Deactivated. 2 rules would allow 1 of 2 items."),
  ).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Remove all access…" }));
  const review = await screen.findByRole("dialog", {
    name: "Remove all access for Alex?",
  });
  await user.click(
    within(review).getByRole("button", { name: "Remove all access" }),
  );
  await waitFor(() =>
    expect(removed).toEqual({
      person_id: "alex",
      review_token: "reviewed-removal",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByText(/deactivated person/)).not.toBeInTheDocument(),
  );
});

it("edits item rules with inherit labels that name the Moment or Album source", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "allow",
    inherited: false,
    effective: true,
    detected: false,
    suggested: false,
    supporting_entries: 0,
    accessible_count: 2,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  const sam = {
    ...alex,
    person_id: "sam",
    display_name: "Sam",
    decision: "",
    inherited: true,
  };
  const current = {
    ...completeAlbum,
    access: [
      { ...alex, decision: "" },
      { ...sam, decision: "allow" },
    ],
    moments: [
      {
        ...completeAlbum.moments[0],
        entries: [
          {
            ...completeAlbum.moments[0].entries[0],
            decisions: { sam: "deny" },
          },
          completeAlbum.moments[0].entries[1],
        ],
        access: { people: [alex, sam], faces: [] },
      },
    ],
  };
  const posts: unknown[] = [];
  mockAPI((path, options) => {
    if (path.endsWith("/entries/photo/rules"))
      posts.push(JSON.parse(String(options?.body)));
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1?entry=photo");
  const user = userEvent.setup();
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Item access" });
  expect(
    within(dialog).getByText(/Decisions for Beach.jpg override/),
  ).toBeVisible();
  expect(
    within(dialog).getByRole("combobox", { name: "Access for Alex" }),
  ).toHaveTextContent("Inherit: allowed by this Moment");
  expect(
    within(dialog).getByRole("combobox", { name: "Access for Sam" }),
  ).toHaveTextContent("Deny");
  await user.click(
    within(dialog).getByRole("combobox", { name: "Access for Sam" }),
  );
  expect(
    screen.getByRole("option", { name: "Inherit: allowed by Album access" }),
  ).toBeVisible();
  await user.click(
    screen.getByRole("option", { name: "Inherit: allowed by Album access" }),
  );
  await user.click(
    within(dialog).getByRole("combobox", { name: "Access for Alex" }),
  );
  await user.click(screen.getByRole("option", { name: "Deny" }));
  await user.click(within(dialog).getByRole("button", { name: "Save access" }));
  await waitFor(() =>
    expect(posts).toEqual([
      {
        decisions: [
          { person_id: "alex", decision: "deny" },
          { person_id: "sam", decision: "inherit" },
        ],
      },
    ]),
  );
});

it("discards item rule edits with one prompt and clears the entry from the URL", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "",
    inherited: false,
    effective: false,
    detected: false,
    suggested: false,
    supporting_entries: 0,
    moments_detected: 0,
    deactivated: false,
    accessible_count: 0,
    exceptions: 0,
  };
  mockAPI(() =>
    Response.json({
      ...completeAlbum,
      moments: [
        { ...completeAlbum.moments[0], access: { people: [alex], faces: [] } },
      ],
    }),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1?entry=photo");
  const user = userEvent.setup();
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Item access" });
  await user.click(
    within(dialog).getByRole("combobox", { name: "Access for Alex" }),
  );
  await user.click(screen.getByRole("option", { name: "Allow" }));
  await user.keyboard("{Escape}");
  const discard = await screen.findByRole("dialog", {
    name: "Discard these access changes?",
  });
  await user.click(within(discard).getByRole("button", { name: "Discard" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(new URLSearchParams(window.location.search).get("entry")).toBeNull();
});

it("opens item details and access from a tile or a single selection without playback controls", async () => {
  desktopViewport();
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Edit Waves.mp4" }),
  );
  const editor = screen.getByRole("dialog", { name: "Video details" });
  expect(within(editor).getByRole("img", { name: "Waves.mp4" })).toBeVisible();
  expect(within(editor).getByText(/Waves.mp4\. The title shows/)).toBeVisible();
  expect(
    within(editor).getByRole("textbox", { name: "Video title" }),
  ).toBeVisible();
  expect(
    within(editor).getByRole("form", { name: "Item access" }),
  ).toBeVisible();
  expect(
    within(editor).queryByRole("button", { name: /play|download/i }),
  ).not.toBeInTheDocument();
  expect(new URLSearchParams(window.location.search).get("entry")).toBe(
    "video",
  );
  await user.keyboard("{Escape}");
  await user.click(screen.getByRole("button", { name: "Select" }));
  await user.click(screen.getByRole("checkbox", { name: "Select Waves.mp4" }));
  await user.click(screen.getByRole("button", { name: "Video details" }));
  expect(screen.getByRole("dialog", { name: "Video details" })).toBeVisible();
});

it("opens saved rules without granting detected people and saves only the edited choice", async () => {
  desktopViewport();
  const sam = {
    person_id: "sam",
    display_name: "Sam",
    avatar_url: "",
    decision: "",
    detected: true,
    suggested: true,
    supporting_entries: 1,
    inherited: false,
    effective: false,
    accessible_count: 0,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  const alex = {
    ...sam,
    person_id: "alex",
    display_name: "Alex",
    decision: "allow",
    suggested: false,
    effective: true,
    accessible_count: 2,
  };
  const current = {
    ...completeAlbum,
    moments: [
      {
        ...completeAlbum.moments[0],
        access: { people: [alex, sam], faces: [] },
      },
    ],
  };
  let saved: unknown;
  mockAPI((path, options) => {
    if (path.endsWith("/rules")) saved = JSON.parse(String(options?.body));
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Rules & exceptions" }),
  );
  expect(
    screen.getByRole("combobox", { name: "Access for Sam" }),
  ).toHaveTextContent("Inherit: no access");
  expect(saved).toBeUndefined();
  expect(
    screen.getByRole("combobox", { name: "Access for Alex" }),
  ).toHaveTextContent("Allow");
  const rulesDialog = screen.getByRole("dialog", {
    name: "Rules & exceptions",
  });
  expect(within(rulesDialog).getByText("Seen in 1 item")).toBeVisible();
  expect(
    within(rulesDialog).getByText("Detected here, not shared yet"),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "Save access" })).toBeEnabled();
  await user.click(screen.getByRole("combobox", { name: "Access for Alex" }));
  await user.click(screen.getByRole("option", { name: "Deny" }));
  await user.click(screen.getByRole("button", { name: "Save access" }));
  await waitFor(() =>
    expect(saved).toEqual({
      decisions: [{ person_id: "alex", decision: "deny" }],
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("keeps a rules draft through refresh and failed save, and asks before discarding it", async () => {
  desktopViewport();
  const sam = {
    person_id: "sam",
    display_name: "Sam",
    avatar_url: "",
    decision: "",
    detected: true,
    suggested: true,
    supporting_entries: 1,
    inherited: false,
    effective: false,
    accessible_count: 0,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  let current = {
    ...completeAlbum,
    moments: [
      { ...completeAlbum.moments[0], access: { people: [sam], faces: [] } },
    ],
  };
  mockAPI((path) =>
    path.endsWith("/rules")
      ? Response.json(
          {
            error: {
              fields: {
                decisions: "Choose an access decision for each person.",
              },
            },
          },
          { status: 400 },
        )
      : Response.json(current),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Rules & exceptions" }),
  );
  await user.click(screen.getByRole("combobox", { name: "Access for Sam" }));
  await user.click(screen.getByRole("option", { name: "Allow" }));
  current = {
    ...current,
    moments: [
      {
        ...current.moments[0],
        access: { people: [{ ...sam, decision: "deny" }], faces: [] },
      },
    ],
  };
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(
    screen.getByRole("combobox", { name: "Access for Sam" }),
  ).toHaveTextContent("Allow");
  await user.click(screen.getByRole("button", { name: "Save access" }));
  expect(
    await screen.findByText("Choose an access decision for each person."),
  ).toBeVisible();
  expect(
    screen.getByRole("combobox", { name: "Access for Sam" }),
  ).toHaveTextContent("Allow");
  fireEvent.click(
    screen.getByRole("link", { name: "Album details", hidden: true }),
  );
  const leave = await screen.findByRole("dialog", { name: "Leave this page?" });
  await user.click(within(leave).getByRole("button", { name: "Cancel" }));
  await user.keyboard("{Escape}");
  const discard = await screen.findByRole("dialog", {
    name: "Discard these access changes?",
  });
  await user.click(within(discard).getByRole("button", { name: "Cancel" }));
  expect(
    screen.getByRole("combobox", { name: "Access for Sam" }),
  ).toHaveTextContent("Allow");
});

it("links an Immich face to an existing Person and derives a suggestion", async () => {
  desktopViewport();
  const face = {
    source_id: "immich-alex",
    source_name: "Immich Alex",
    thumbnail_url: "/api/media/faces/immich-alex/thumbnail?v=1",
    immich_url: "http://immich.test/people/immich-alex",
    person_id: "",
    person_name: "",
    ignored: false,
    occurrences: 2,
  };
  const alex = {
    id: "alex",
    display_name: "Alex",
    is_curator: false,
    onboarding_completed_at: null,
    deactivated_at: null,
    update_email: "",
    email_updates: true,
    avatar_url: "",
  };
  let current: AlbumDetail = {
    ...completeAlbum,
    moments: [
      {
        ...completeAlbum.moments[0],
        access: { people: [], faces: [face] },
      },
    ],
  };
  const posts: Array<{ path: string; body: unknown }> = [];
  mockAPI((path, options) => {
    if (path === "/api/people?q=") return Response.json([alex]);
    if (options?.method === "POST") {
      const body: unknown = JSON.parse(String(options.body));
      posts.push({ path, body });
      if (path === "/api/people/alex/faces") {
        current = {
          ...current,
          moments: current.moments.map((moment) => ({
            ...moment,
            access: {
              ...moment.access,
              faces: [{ ...face, person_id: "alex", person_name: "Alex" }],
              people: [
                {
                  person_id: "alex",
                  display_name: "Alex",
                  avatar_url: "",
                  decision: "",
                  detected: true,
                  suggested: true,
                  supporting_entries: 2,
                  inherited: false,
                  effective: false,
                  accessible_count: 0,
                  exceptions: 0,
                  moments_detected: 0,
                  deactivated: false,
                },
              ],
            },
          })),
        };
        return Response.json({
          person: alex,
          faces: [
            {
              source_face_id: face.source_id,
              source_name: face.source_name,
              thumbnail_url: face.thumbnail_url,
              avatar: false,
            },
          ],
          identities: [],
          preauthorizations: [],
          sessions: [],
        });
      }
    }
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);

  await user.click(await screen.findByText("1 unlinked face to link"));
  const faces = screen.getByRole("region", { name: "Unlinked faces" });
  expect(
    screen.queryByRole("combobox", { name: "Person" }),
  ).not.toBeInTheDocument();
  await user.click(
    within(faces).getByRole("button", { name: "Link Immich Alex" }),
  );
  await user.click(screen.getByRole("combobox", { name: "Person" }));
  expect(
    screen.getByRole("option", { name: 'Create "Immich Alex"' }),
  ).toBeVisible();
  await user.click(screen.getByRole("option", { name: "Alex" }));
  await user.click(screen.getByRole("link", { name: "Album details" }));
  await user.click(
    within(
      await screen.findByRole("dialog", { name: "Leave this page?" }),
    ).getByRole("button", { name: "Cancel" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("combobox", { name: "Person" })).toHaveTextContent(
    "Alex",
  );
  await user.click(screen.getByRole("button", { name: "Link face" }));
  await waitFor(() =>
    expect(posts).toContainEqual({
      path: "/api/people/alex/faces",
      body: { source_face_id: "immich-alex" },
    }),
  );
  const suggested = await screen.findByRole("region", { name: /Suggested/ });
  expect(
    within(suggested).getByText("Detected here, not shared yet"),
  ).toBeVisible();
  expect(
    within(screen.getByRole("region", { name: "Moment access" })).queryByText(
      /unlinked face/,
    ),
  ).not.toBeInTheDocument();
});

it("keeps ignored faces reachable when no unlinked faces remain", async () => {
  desktopViewport();
  mockAPI(() =>
    Response.json({
      ...completeAlbum,
      moments: [
        {
          ...completeAlbum.moments[0],
          access: {
            people: [],
            faces: [
              {
                source_id: "stranger",
                source_name: "",
                thumbnail_url: "",
                immich_url: "",
                person_id: "",
                person_name: "",
                ignored: true,
                occurrences: 1,
              },
            ],
          },
        },
      ],
    }),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByText("1 ignored face"));
  await user.click(screen.getByText("Ignored faces (1)"));
  expect(
    screen.getByRole("button", { name: "Link Unnamed face" }),
  ).toBeVisible();
});

it("disables unsupported imports without hiding the library or blocking imported albums", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (path.endsWith("/connection"))
        return Response.json({
          usable: true,
          import_supported: false,
          version: "2.6.0",
          message: "Imports require Immich 2.7.5 or later.",
        });
      if (path.endsWith("/albums/album-1")) return Response.json(completeAlbum);
      return Response.json({
        albums: [
          source,
          {
            ...source,
            id: "old",
            title: "Previously imported",
            album_id: album.id,
          },
        ],
        page: 1,
        pages: 1,
        total: 2,
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Imports require Immich 2.7.5 or later.",
  );
  expect(
    await screen.findByRole("link", { name: "Open album" }),
  ).toHaveAttribute("href", "/curator/albums/album-1");
  expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  expect(screen.getByRole("heading", { name: source.title })).toBeVisible();
  await user.click(screen.getByRole("link", { name: "Open album" }));
  await user.click(await screen.findByRole("link", { name: "Album details" }));
  expect(
    await screen.findByRole("textbox", { name: "Album title" }),
  ).toHaveValue(album.title);
});

it("keeps title edits through failed refresh, focuses field errors, and saves only the owned title", async () => {
  let current = completeAlbum;
  let failRead = false;
  let failSave = true;
  mockAPI((path, options) => {
    if (options?.method === "POST") {
      if (failSave)
        return Response.json(
          { error: { fields: { title: "Enter an album title." } } },
          { status: 400 },
        );
      expect(JSON.parse(String(options.body))).toEqual({ title: "Our summer" });
      current = { ...current, title: "Our summer" };
    } else if (failRead) return Response.json({}, { status: 503 });
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("link", { name: "Album details" }));
  const title = await screen.findByRole("textbox", { name: "Album title" });
  await user.clear(title);
  await user.type(title, "Our summer");
  expect(screen.getByText("A week away")).toBeVisible();
  expect(
    screen.getByRole("textbox", { name: "Description from Immich" }),
  ).toHaveAttribute("readonly");
  failRead = true;
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong.",
  );
  expect(title).toHaveValue("Our summer");
  failRead = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  await user.click(screen.getByRole("button", { name: "Save title" }));
  expect(await screen.findByText("Enter an album title.")).toBeVisible();
  expect(title).toHaveFocus();
  failSave = false;
  await user.click(screen.getByRole("button", { name: "Save title" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Title saved.");
  expect(screen.getByRole("heading", { name: "Our summer" })).toBeVisible();
  expect(document.title).toBe("Our summer | Memento");
});

it.each(["complete", "failed"])(
  "stops polling when import is %s",
  async (terminal) => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let current = {
      ...completeAlbum,
      status: "processing",
      processed: 12,
      moments: [],
    };
    let reads = 0;
    mockAPI(() => {
      reads++;
      return Response.json(current);
    });
    window.history.replaceState(null, "", "/curator/albums/album-1");
    render(<App />);
    expect(await screen.findByRole("progressbar")).toHaveAttribute(
      "value",
      "12",
    );
    current = { ...current, status: terminal, processed: 36 };
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() =>
      expect(screen.queryByRole("progressbar")).not.toBeInTheDocument(),
    );
    expect(
      terminal === "complete"
        ? screen.getByRole("navigation", { name: "Album outline" })
        : screen.getByRole("button", { name: "Retry import" }),
    ).toBeVisible();
    const stoppedAt = reads;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(reads).toBe(stoppedAt);
  },
);

it("stops polling when leaving a running import", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  let reads = 0;
  mockAPI((path) => {
    if (path.endsWith("/albums")) return Response.json([]);
    reads++;
    return Response.json({ ...album, status: "processing", moments: [] });
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("progressbar");
  await user.click(screen.getByRole("link", { name: "All albums" }));
  await screen.findByRole("heading", { name: "Your albums" });
  const stoppedAt = reads;
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(reads).toBe(stoppedAt);
});

it("retries an interrupted import and keeps showing recovery status until it resumes", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  let current = { ...album, status: "interrupted", moments: [] };
  mockAPI((path, options) => {
    if (options?.method === "POST") {
      expect(path).toBe("/api/curator/albums/album-1/retry");
      current = { ...current, status: "queued" };
    }
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Import interrupted" }),
  ).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Retry import" }));
  expect(
    await screen.findByRole("heading", { name: "Waiting to import" }),
  ).toBeVisible();
  current = { ...current, status: "processing" };
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(
    await screen.findByRole("heading", { name: "Importing your album" }),
  ).toBeVisible();
});

it("recovers from a source read failure and explains an empty search", async () => {
  let failed = true;
  mockAPI(() =>
    failed
      ? Response.json({}, { status: 503 })
      : Response.json({ albums: [], page: 1, pages: 0, total: 0 }),
  );
  window.history.replaceState(null, "", "/curator/import?q=absent&page=1");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong.",
  );
  failed = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect(
    await screen.findByRole("heading", { name: "No matching albums" }),
  ).toBeVisible();
  expect(
    screen.getByRole("searchbox", { name: "Search Immich albums" }),
  ).toHaveValue("absent");
});

it("shows a failed import honestly in the album list and lets the Curator reopen it", async () => {
  mockAPI(() => Response.json([{ ...album, status: "failed" }]));
  window.history.replaceState(null, "", "/curator");
  render(<App />);
  const link = await screen.findByRole("link", { name: /Summer by the sea/ });
  expect(link).toHaveTextContent("Import failed");
  expect(link).toHaveAttribute("href", "/curator/albums/album-1");
});

it("pages back from the server-clamped page instead of an out-of-range bookmark", async () => {
  mockAPI((path) => {
    const requested = Number(
      new URL(path, window.location.origin).searchParams.get("page"),
    );
    return Response.json({
      albums: [source],
      page: Math.min(requested, 3),
      pages: 3,
      total: 60,
    });
  });
  window.history.replaceState(null, "", "/curator/import?q=Summer&page=999");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByText("Page 3 of 3")).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Next page" }),
  ).not.toBeInTheDocument();
  await user.click(screen.getByRole("link", { name: "Previous page" }));
  expect(await screen.findByText("Page 2 of 3")).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("page")).toBe("2");
  expect(new URLSearchParams(window.location.search).get("q")).toBe("Summer");
});

it("replaces a broken source cover and tries a refreshed cover URL", async () => {
  let cover = source.cover_url;
  mockAPI(() =>
    Response.json({
      albums: [{ ...source, cover_url: cover }],
      page: 1,
      pages: 1,
      total: 1,
    }),
  );
  window.history.replaceState(null, "", "/curator/import");
  render(<App />);
  fireEvent.error(await screen.findByRole("img", { name: source.title }));
  expect(screen.getByText("No cover available")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: source.title }),
  ).not.toBeInTheDocument();
  cover = "/api/curator/sources/summer/cover?v=2";
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(
    await screen.findByRole("img", { name: source.title }),
  ).toHaveAttribute("src", cover);
  expect(screen.queryByText("No cover available")).not.toBeInTheDocument();
});

it("replaces failed imported thumbnails while keeping capture times and other previews", async () => {
  desktopViewport();
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  render(<App />);
  fireEvent.error(await screen.findByRole("img", { name: "Beach.jpg" }));
  expect(screen.getByText("No preview available")).toBeVisible();
  expect(screen.getByText("12:00 PM")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: "Beach.jpg" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("img", { name: "Waves.mp4" })).toBeVisible();
});

it.each(["complete", "failed"])(
  "refreshes active album-list statuses and stops when all imports finish with %s",
  async (terminal) => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let status = "queued";
    let reads = 0;
    mockAPI(() => {
      reads++;
      return Response.json([
        { ...album, id: "finished", title: "Already imported" },
        { ...album, status },
      ]);
    });
    window.history.replaceState(null, "", "/curator");
    render(<App />);
    const link = await screen.findByRole("link", { name: /Summer by the sea/ });
    expect(link).toHaveTextContent("Waiting to import");
    expect(reads).toBe(1);
    status = "processing";
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() => expect(link).toHaveTextContent("Import in progress"));
    expect(reads).toBe(2);
    status = "interrupted";
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() => expect(link).toHaveTextContent("Import interrupted"));
    expect(reads).toBe(3);
    status = terminal;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() =>
      expect(link).toHaveTextContent(
        terminal === "complete" ? "35 photos, 1 video" : "Import failed",
      ),
    );
    expect(reads).toBe(4);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(reads).toBe(4);
  },
);

it("stops album-list polling when navigating away from active imports", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  let reads = 0;
  mockAPI((path) => {
    if (path === "/api/curator/albums") {
      reads++;
      return Response.json([{ ...album, status: "processing" }]);
    }
    return Response.json({ albums: [], page: 1, pages: 0, total: 0 });
  });
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("link", { name: /Summer by the sea/ });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(reads).toBe(2);
  await user.click(screen.getByRole("link", { name: "Import an album" }));
  await screen.findByRole("heading", { name: "Import an album" });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(reads).toBe(2);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  focusManager.setFocused(undefined);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

it("browses source covers, searches in the URL and keeps search when paging", async () => {
  mockAPI((path) => {
    const url = new URL(path, window.location.origin);
    return Response.json({
      albums: [
        {
          ...source,
          title:
            url.searchParams.get("page") === "2"
              ? "Summer at home"
              : source.title,
        },
      ],
      page: Number(url.searchParams.get("page") || 1),
      pages: 2,
      total: 25,
    });
  });
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: source.title }),
  ).toBeVisible();
  expect(screen.getByRole("img", { name: source.title })).toHaveAttribute(
    "src",
    source.cover_url,
  );
  expect(screen.getByText("36 items")).toBeVisible();
  expect(screen.getByText(/Jul 1, 2026.*Jul 7, 2026/)).toBeVisible();
  await user.type(
    screen.getByRole("searchbox", { name: "Search Immich albums" }),
    "Summer{Enter}",
  );
  expect(new URLSearchParams(window.location.search).get("q")).toBe("Summer");
  await user.click(await screen.findByRole("link", { name: "Next page" }));
  expect(
    await screen.findByRole("heading", { name: "Summer at home" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("q")).toBe("Summer");
  expect(new URLSearchParams(window.location.search).get("page")).toBe("2");
});

it("asks before discarding an edited Moment title and keeps the field focused when the Curator stays", async () => {
  desktopViewport();
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const moment = await screen.findByRole("region", { name: "First day" });
  await user.click(within(moment).getByRole("button", { name: "Rename" }));
  const title = screen.getByRole("textbox", { name: "Moment title" });
  await user.type(title, " at the beach");
  await user.keyboard("{Escape}");
  const discard = await screen.findByRole("dialog", {
    name: "Discard this Moment title?",
  });
  await user.click(within(discard).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(discard).not.toBeInTheDocument());
  expect(screen.getByRole("dialog", { name: "Rename Moment" })).toBeVisible();
  expect(title).toHaveValue("First day at the beach");
  expect(title).toHaveFocus();
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  await user.click(
    within(
      await screen.findByRole("dialog", { name: "Discard this Moment title?" }),
    ).getByRole("button", { name: "Discard" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(within(moment).getByRole("button", { name: "Rename" })).toBeVisible();
});
