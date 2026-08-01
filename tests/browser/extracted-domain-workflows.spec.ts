import { expect, test, type Page, type Route } from "@playwright/test";

const csrfToken = "c".repeat(64);
const sourceID = "11111111-1111-4111-8111-111111111111";

type RecordedRequest = {
  body: unknown;
  headers: Record<string, string>;
  method: string;
  path: string;
};

function recordRequest(route: Route): RecordedRequest {
  const request = route.request();
  return {
    body: request.postData() ? (request.postDataJSON() as unknown) : null,
    headers: request.headers(),
    method: request.method(),
    path: new URL(request.url()).pathname,
  };
}

async function expectNoHorizontalOverflow(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(() => ({
        clientWidth: document.documentElement.clientWidth,
        scrollWidth: document.documentElement.scrollWidth,
      })),
    )
    .toEqual(
      await page.evaluate(() => ({
        clientWidth: document.documentElement.clientWidth,
        scrollWidth: document.documentElement.clientWidth,
      })),
    );
}

async function fulfillCuratorShellRequest(route: Route) {
  const path = new URL(route.request().url()).pathname;
  if (path === "/api/sessions") {
    await route.fulfill({ json: { sessions: [] } });
    return;
  }
  if (path === "/api/people") {
    await route.fulfill({ json: { people: [] } });
    return;
  }
  if (path === "/api/relationships") {
    await route.fulfill({ json: { relationships: [] } });
    return;
  }
  if (path === "/api/visibility-circles") {
    await route.fulfill({ json: { circles: [] } });
    return;
  }
  if (path === "/api/sources") {
    await route.fulfill({ json: { albums: [], next_cursor: null } });
    return;
  }
  if (path === "/api/repairs") {
    await route.fulfill({
      json: {
        person_candidates: [],
        media_candidates: [],
        unlinked_immich_people: [],
      },
    });
    return;
  }
  if (path === "/api/activity/curator/work") {
    await route.fulfill({ json: { items: [] } });
    return;
  }
  if (path === "/api/invitation-suggestions") {
    await route.fulfill({ json: { suggestions: [] } });
    return;
  }
  if (path === "/api/invitation-suggestions/curator") {
    await route.fulfill({ json: { suggestions: [] } });
    return;
  }
  await route.fulfill({
    status: 404,
    json: { error: { message: `No evidence fixture for ${path}` } },
  });
}

async function fulfillRecipientShellRequest(route: Route) {
  const path = new URL(route.request().url()).pathname;
  if (path === "/api/me/photos") {
    await route.fulfill({ json: { media: [], next_cursor: null } });
    return;
  }
  if (path === "/api/me/new-for-you") {
    await route.fulfill({ json: { events: [] } });
    return;
  }
  if (path === "/api/sessions") {
    await route.fulfill({ json: { sessions: [] } });
    return;
  }
  if (path === "/api/me/interest-list") {
    await route.fulfill({
      json: {
        recipient: {
          id: "22222222-2222-4222-8222-222222222222",
          display_name: "Alex",
          sort_name: "alex",
        },
        version: 0,
        entries: [],
        history: [],
      },
    });
    return;
  }
  if (path === "/api/me/people/search") {
    await route.fulfill({ json: { people: [], next_cursor: null } });
    return;
  }
  if (path === "/api/invitation-suggestions") {
    await route.fulfill({ json: { suggestions: [] } });
    return;
  }
  if (path === "/api/me/engagement") {
    await route.fulfill({ status: 204 });
    return;
  }
  await route.fulfill({
    status: 404,
    json: { error: { message: `No evidence fixture for ${path}` } },
  });
}

function sourceAlbum(
  disposition: "drafted" | "ignored" | "unreviewed",
  version: number,
) {
  return {
    id: sourceID,
    name: "Family trip",
    description: "A private owned album",
    asset_count: 7,
    source_created_at: "2026-01-01T00:00:00Z",
    source_updated_at: "2026-02-01T00:00:00Z",
    start_at: "2026-01-01T00:00:00Z",
    end_at: "2026-01-07T00:00:00Z",
    disposition,
    version,
    first_seen_at: "2026-03-01T00:00:00Z",
    last_seen_at: "2026-03-02T00:00:00Z",
    source_missing: false,
  };
}

test("@desktop @mobile completes first-browser Setup with the same choices", async ({
  page,
}) => {
  const requests: RecordedRequest[] = [];
  let setupComplete = false;
  await page.route("**/api/**", async (route) => {
    const recorded = recordRequest(route);
    requests.push(recorded);
    if (recorded.path === "/api/setup") {
      await route.fulfill({ json: { status: "available" } });
      return;
    }
    if (recorded.path === "/api/setup/code") {
      await route.fulfill({
        status: 202,
        json: { challenge_id: "a".repeat(64), status: "queued" },
      });
      return;
    }
    if (recorded.path === "/api/setup/verify") {
      await route.fulfill({
        json: { verification_token: "b".repeat(64), status: "verified" },
      });
      return;
    }
    if (recorded.path === "/api/setup/complete") {
      setupComplete = true;
      await route.fulfill({
        status: 201,
        json: { status: "complete", csrf_token: csrfToken },
      });
      return;
    }
    if (recorded.path === "/api/session" && setupComplete) {
      await route.fulfill({
        json: {
          display_name: "Robin Joseph",
          session_type: "public",
          csrf_token: csrfToken,
          curator: true,
          onboarding_required: false,
        },
      });
      return;
    }
    await fulfillCuratorShellRequest(route);
  });

  await page.goto("/");
  await page.getByLabel("Your name").fill("Robin Joseph");
  await page.getByLabel("Login email").fill("robin@example.com");
  await page.getByRole("button", { name: "Send verification code" }).click();
  await expect(
    page.getByRole("heading", { name: "Verify your email" }),
  ).toBeFocused();

  await page.getByLabel("Verification code").fill("12345678");
  await page.getByRole("button", { name: "Verify email" }).click();
  await expect(
    page.getByRole("heading", { name: "Choose how Memento works for you" }),
  ).toBeFocused();
  for (const label of [
    /Private individual access/,
    /Curator-visible engagement/,
    /Interest list starts empty/,
    /Private email previews/,
    /Push is an optional device choice/,
  ]) {
    await page.getByLabel(label).check();
  }
  await page.getByLabel("Publication and Comment email").selectOption("weekly");
  await page
    .getByRole("radio", { name: /Public computer, expires within 12 hours/ })
    .check();
  const complete = page.getByRole("button", { name: "Complete setup" });
  await complete.focus();
  await expect(complete).toBeFocused();
  await complete.click();

  await expect(
    page.getByText("Setup is complete. You're signed in as Robin Joseph."),
  ).toBeVisible();
  expect(requests.find(({ path }) => path === "/api/setup/code")).toMatchObject(
    {
      method: "POST",
      body: { display_name: "Robin Joseph", email: "robin@example.com" },
    },
  );
  expect(
    requests.find(({ path }) => path === "/api/setup/verify"),
  ).toMatchObject({
    method: "POST",
    body: { challenge_id: "a".repeat(64), code: "12345678" },
  });
  const completion = requests.find(
    ({ path }) => path === "/api/setup/complete",
  );
  expect(completion).toMatchObject({
    method: "POST",
    body: {
      verification_token: "b".repeat(64),
      privacy_acknowledged: true,
      engagement_acknowledged: true,
      interest_list_acknowledged: true,
      email_previews_acknowledged: true,
      push_guidance_acknowledged: true,
      email_preference: "weekly",
      session_type: "public",
    },
  });
  expect(completion?.headers["x-memento-csrf"]).toBeUndefined();
  expect(
    requests.filter(({ method }) => method !== "GET").map(({ path }) => path),
  ).toEqual(["/api/setup/code", "/api/setup/verify", "/api/setup/complete"]);
  await expectNoHorizontalOverflow(page);
});

test("@desktop @mobile saves and reloads Recipient email notification settings", async ({
  page,
}) => {
  const mutations: RecordedRequest[] = [];
  const updates: RecordedRequest[] = [];
  let preferences = {
    email_preference: "immediate",
    weekly_day: "sunday",
    weekly_local_time: "09:00",
    weekly_timezone: "UTC",
  };
  await page.route("**/api/**", async (route) => {
    const recorded = recordRequest(route);
    if (recorded.method !== "GET") mutations.push(recorded);
    if (recorded.path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "Setup not found." } },
      });
      return;
    }
    if (recorded.path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Alex",
          session_type: "public",
          csrf_token: csrfToken,
          curator: false,
          onboarding_required: false,
        },
      });
      return;
    }
    if (recorded.path === "/api/me/email-preferences") {
      if (recorded.method === "PUT") {
        updates.push(recorded);
        preferences = recorded.body as typeof preferences;
      }
      await route.fulfill({ json: preferences });
      return;
    }
    await fulfillRecipientShellRequest(route);
  });

  const openPreferences = async () => {
    await page.getByLabel("Account for Alex").click();
    await page
      .getByRole("button", { name: "Manage email preferences" })
      .click();
  };

  await page.goto("/");
  await openPreferences();
  await page.getByLabel("Publication and Comment email").selectOption("weekly");
  await page.getByLabel("Day").selectOption("wednesday");
  await page.getByLabel("Local time").fill("18:45");
  await page.getByLabel("Timezone").fill("Europe/London");
  const save = page.getByRole("button", { name: "Save email preferences" });
  await save.focus();
  await expect(save).toBeFocused();
  await save.click();

  await expect(
    page.getByText("Your email preferences were saved."),
  ).toBeVisible();
  expect(updates).toHaveLength(1);
  expect(updates[0]).toMatchObject({
    method: "PUT",
    body: {
      email_preference: "weekly",
      weekly_day: "wednesday",
      weekly_local_time: "18:45",
      weekly_timezone: "Europe/London",
    },
  });
  expect(updates[0].headers["x-memento-csrf"]).toBe(csrfToken);
  expect(
    mutations
      .filter(
        ({ path }) =>
          path !== "/api/me/engagement" && path !== "/api/me/people/search",
      )
      .map(({ path }) => path),
  ).toEqual(["/api/me/email-preferences"]);
  expect(
    mutations.some(({ path }) => /access|audience|recipients/.test(path)),
  ).toBe(false);

  await page.reload();
  await openPreferences();
  await expect(page.getByLabel("Publication and Comment email")).toHaveValue(
    "weekly",
  );
  await expect(page.getByLabel("Day")).toHaveValue("wednesday");
  await expect(page.getByLabel("Local time")).toHaveValue("18:45");
  await expect(page.getByLabel("Timezone")).toHaveValue("Europe/London");
  await expectNoHorizontalOverflow(page);
});

test("@desktop @mobile discovers and triages the private Curator Source inbox", async ({
  page,
}) => {
  const mutations: RecordedRequest[] = [];
  const immichRequests: string[] = [];
  let disposition: "ignored" | "unreviewed" = "unreviewed";
  let version = 1;
  page.on("request", (request) => {
    if (/immich/i.test(request.url())) immichRequests.push(request.url());
  });
  await page.route("**/api/**", async (route) => {
    const recorded = recordRequest(route);
    if (recorded.method !== "GET") mutations.push(recorded);
    if (recorded.path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "Setup not found." } },
      });
      return;
    }
    if (recorded.path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Robin",
          session_type: "public",
          csrf_token: csrfToken,
          curator: true,
          onboarding_required: false,
        },
      });
      return;
    }
    if (recorded.path === "/api/sources/discover") {
      await route.fulfill({
        status: 202,
        json: { status: "connected", discovered_count: 1 },
      });
      return;
    }
    if (recorded.path === `/api/sources/${sourceID}/ignore`) {
      disposition = "ignored";
      version += 1;
      await route.fulfill({ json: sourceAlbum(disposition, version) });
      return;
    }
    if (recorded.path === "/api/sources") {
      const requestedDisposition = new URL(
        route.request().url(),
      ).searchParams.get("disposition");
      await route.fulfill({
        json: {
          albums:
            requestedDisposition === disposition
              ? [sourceAlbum(disposition, version)]
              : [],
          next_cursor: null,
        },
      });
      return;
    }
    await fulfillCuratorShellRequest(route);
  });

  await page.goto("/");
  await expect(page.getByText("Family trip", { exact: true })).toBeVisible();
  const discover = page.getByRole("button", { name: "Connect and discover" });
  await discover.focus();
  await expect(discover).toBeFocused();
  await discover.click();
  await expect(
    page.getByText("Immich v3.0.3 connected. Found 1 owned album."),
  ).toBeVisible();

  await page.getByRole("button", { name: "Inspect Family trip" }).click();
  await expect(page.getByText("A private owned album")).toBeVisible();
  await page.getByRole("button", { name: "Ignore Source album" }).click();
  await expect(page.getByText("Family trip", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("status")).toHaveText("Ignored Family trip.");

  expect(mutations).toHaveLength(2);
  expect(mutations[0]).toMatchObject({
    method: "POST",
    path: "/api/sources/discover",
    body: null,
  });
  expect(mutations[0].headers["x-memento-csrf"]).toBe(csrfToken);
  expect(mutations[1]).toMatchObject({
    method: "POST",
    path: `/api/sources/${sourceID}/ignore`,
    body: null,
  });
  expect(mutations[1].headers["x-memento-csrf"]).toBe(csrfToken);
  expect(mutations[1].headers["if-match"]).toBe('"1"');
  expect(immichRequests).toEqual([]);
  await expectNoHorizontalOverflow(page);
});

test("@desktop @mobile drafts combined Source Media into private Events and Loose items", async ({
  page,
}) => {
  const secondSourceID = "22222222-2222-4222-8222-222222222222";
  const datedMedia = {
    id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    media_type: "image",
    width: 1200,
    height: 800,
    local_date_time: "2026-06-01T12:00:00",
  };
  const undatedMedia = {
    id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    media_type: "video",
    width: 1920,
    height: 1080,
    local_date_time: null,
  };
  const unusedMedia = {
    id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
    media_type: "image",
    width: 800,
    height: 800,
    local_date_time: "2026-06-02T09:00:00",
  };
  const eventID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee";
  const momentID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";
  let currentEvent = {
    id: eventID,
    lifecycle: "draft",
    title: "Combined Source draft",
    description: "",
    place_labels: [],
    grouping_timezone: "UTC",
    version: 1,
    final_review_complete: false,
    published_editable_version: null,
    published_attendance_recovery_required: false,
    pending_withdrawal_publication: false,
    staged_update: null,
    sources: [
      {
        id: sourceID,
        metadata_suggestion: {
          name: "Updated Family trip",
          description: "A suggested Source description",
        },
      },
      { id: secondSourceID, metadata_suggestion: null },
    ],
    moments: [
      {
        id: momentID,
        title: "",
        place_labels: [],
        proposed_day: "2026-06-01",
        grouping_timezone: "UTC",
        source_days: ["2026-06-01"],
        proposal_kind: "local_day",
        cover_media_item_id: datedMedia.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [datedMedia],
      },
    ],
    unassigned_media: [undatedMedia],
    withdrawal_targets: [],
    withdrawals: [],
    created_at: "2026-06-03T00:00:00Z",
    updated_at: "2026-06-03T00:00:00Z",
  };
  const mutations: RecordedRequest[] = [];
  await page.route("**/api/**", async (route) => {
    const recorded = recordRequest(route);
    if (recorded.method !== "GET") mutations.push(recorded);
    if (recorded.path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "Setup not found." } },
      });
      return;
    }
    if (recorded.path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Robin",
          session_type: "public",
          csrf_token: csrfToken,
          curator: true,
          onboarding_required: false,
        },
      });
      return;
    }
    if (recorded.path === "/api/sources") {
      const disposition = new URL(route.request().url()).searchParams.get(
        "disposition",
      );
      await route.fulfill({
        json: {
          albums:
            disposition === "unreviewed"
              ? [sourceAlbum("unreviewed", 1)]
              : disposition === "drafted"
                ? [
                    {
                      ...sourceAlbum("drafted", 1),
                      id: secondSourceID,
                      name: "Family reunion",
                    },
                  ]
                : [],
          next_cursor: null,
        },
      });
      return;
    }
    if (recorded.path === `/api/sources/${sourceID}/media-items`) {
      await route.fulfill({
        json: { media_items: [datedMedia, unusedMedia] },
      });
      return;
    }
    if (recorded.path === `/api/sources/${secondSourceID}/media-items`) {
      await route.fulfill({
        json: { media_items: [datedMedia, undatedMedia] },
      });
      return;
    }
    if (recorded.path === "/api/loose-items") {
      await route.fulfill({
        status: 201,
        json: {
          id: "ffffffff-ffff-4fff-8fff-ffffffffffff",
          lifecycle: "draft",
          title: "",
          description: "",
          grouping_timezone: "UTC",
          proposed_day: null,
          version: 1,
          audience_complete: false,
          media_item: undatedMedia,
          created_at: "2026-06-03T00:00:00Z",
          updated_at: "2026-06-03T00:00:00Z",
        },
      });
      return;
    }
    if (recorded.path === "/api/events" && recorded.method === "POST") {
      await route.fulfill({ status: 201, json: currentEvent });
      return;
    }
    if (recorded.path === "/api/events" && recorded.method === "GET") {
      await route.fulfill({
        json: {
          events: [
            {
              id: eventID,
              lifecycle: "draft",
              title: currentEvent.title,
              version: currentEvent.version,
              moment_count: 1,
              unassigned_count: 1,
              has_staged_update: false,
              updated_at: currentEvent.updated_at,
            },
          ],
        },
      });
      return;
    }
    if (recorded.path === `/api/events/${eventID}`) {
      await route.fulfill({ json: currentEvent });
      return;
    }
    if (recorded.path === `/api/events/${eventID}/organization`) {
      const body = recorded.body as {
        title: string;
        description: string;
        grouping_timezone: string;
      };
      currentEvent = {
        ...currentEvent,
        title: body.title,
        description: body.description,
        grouping_timezone: body.grouping_timezone,
        version: currentEvent.version + 1,
      };
      await route.fulfill({ json: currentEvent });
      return;
    }
    await fulfillCuratorShellRequest(route);
  });

  await page.goto("/");
  await page.getByLabel("Select Family trip for drafting").check();
  await page.getByRole("button", { name: "Drafted" }).click();
  await expect(page).toHaveURL(/source_view=drafted/);
  await page.getByLabel("Select Family reunion for drafting").check();
  await expect(page.getByText("2 Source albums selected")).toBeVisible();
  await page
    .getByRole("button", { name: "Draft selected Source albums" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Draft Source Media" }),
  ).toBeVisible();
  const mobileLayout = (page.viewportSize()?.width ?? 1000) <= 864;
  if (mobileLayout) {
    expect(await page.evaluate(() => window.innerWidth)).toBeLessThanOrEqual(
      864,
    );
    await expect(page.locator(".source-drafting")).toHaveAttribute(
      "data-layout",
      "drill-down",
    );
    await expect(
      page.getByRole("heading", { name: "Source Media", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "Draft details" }),
    ).toHaveCount(0);
  }

  await page.getByRole("button", { name: "Details", exact: true }).click();
  await expect(page.locator("#source-draft-details")).toBeFocused();
  if (mobileLayout)
    await expect(
      page.getByRole("heading", { name: "Selected Sources" }),
    ).toHaveCount(0);
  await page.getByLabel("Loose item").check();
  await page.getByRole("button", { name: /Media \(3\)/ }).click();
  await expect(page.locator("#source-draft-media")).toBeFocused();
  await page.getByLabel(/Select undated video Media/).check();
  await page.getByRole("button", { name: "Details", exact: true }).click();
  await page.getByLabel("Grouping timezone").fill("UTC");
  await page.getByRole("button", { name: "Create private Loose item" }).click();
  await expect(
    page.getByText(
      "Loose item is ready privately. Review its Audience before Publication.",
    ),
  ).toBeVisible();

  await page.getByLabel("Event").check();
  await page.getByLabel("Choose current Media").check();
  await page.getByRole("button", { name: /Media \(3\)/ }).click();
  await page.getByLabel(/Select photo Media aaaaaaaa/).check();
  await page.getByLabel(/Select undated video Media/).check();
  await expect(
    page.getByText("1 item will remain private and unused."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Details", exact: true }).click();
  await page.getByLabel("Event title").fill("Combined Source draft");
  await page
    .getByRole("button", { name: "Create private Event draft" })
    .click();

  await expect(
    page.getByRole("heading", { name: "Organize drafts" }),
  ).toBeVisible();
  await expect(page.getByText("Source metadata suggestions")).toBeVisible();
  await expect(page.getByLabel("Event title")).toHaveValue(
    "Combined Source draft",
  );
  await page
    .getByRole("button", { name: "Use suggested title Updated Family trip" })
    .click();
  await expect
    .poll(
      () =>
        mutations.filter(
          ({ path }) => path === `/api/events/${eventID}/organization`,
        ).length,
    )
    .toBe(1);
  await expect(page.getByText("All changes saved")).toBeVisible();
  await expect(page.getByLabel("Event title")).toHaveValue(
    "Updated Family trip",
  );

  const looseCreation = mutations.find(
    ({ path }) => path === "/api/loose-items",
  );
  expect(looseCreation).toMatchObject({
    method: "POST",
    body: {
      media_item_id: undatedMedia.id,
      timezone: "UTC",
      title: "",
      description: "",
    },
  });
  expect(looseCreation?.headers["x-memento-csrf"]).toBe(csrfToken);
  const eventCreation = mutations.find(
    ({ path, method }) => path === "/api/events" && method === "POST",
  );
  expect(eventCreation).toMatchObject({
    body: {
      source_album_ids: [sourceID, secondSourceID],
      media_item_ids: [datedMedia.id, undatedMedia.id],
      idempotency_key: expect.any(String),
      timezone: "UTC",
      title: "Combined Source draft",
      description: "",
    },
  });
  expect(eventCreation?.headers["x-memento-csrf"]).toBe(csrfToken);
  const organization = mutations.find(
    ({ path }) => path === `/api/events/${eventID}/organization`,
  );
  expect(organization).toMatchObject({
    method: "PUT",
    body: { title: "Updated Family trip" },
  });
  expect(organization?.headers["x-memento-csrf"]).toBe(csrfToken);
  expect(
    mutations.some(({ path }) =>
      /publications|recipients|new-for-you|notifications|outbox/.test(path),
    ),
  ).toBe(false);
  await expectNoHorizontalOverflow(page);
});

test("@desktop @mobile reviews and confirms Recovery-hold release", async ({
  page,
}) => {
  let allowConfirmation!: () => void;
  const confirmationAllowed = new Promise<void>((resolve) => {
    allowConfirmation = resolve;
  });
  const mutations: RecordedRequest[] = [];
  let released = false;
  await page.route("**/api/**", async (route) => {
    const recorded = recordRequest(route);
    if (recorded.method !== "GET") mutations.push(recorded);
    if (recorded.path === "/api/setup") {
      if (!released) {
        await route.fulfill({
          status: 503,
          json: { error: { message: "Recovery hold is active." } },
        });
      } else {
        await confirmationAllowed;
        await route.fulfill({
          status: 404,
          json: { error: { message: "Setup not found." } },
        });
      }
      return;
    }
    if (recorded.path === "/api/recovery/status") {
      await route.fulfill({ json: { held: true } });
      return;
    }
    if (recorded.path === "/api/session") {
      if (released) {
        await route.fulfill({
          status: 401,
          json: { error: { message: "Fresh sign-in required." } },
        });
      } else {
        await route.fulfill({
          json: {
            display_name: "Robin",
            session_type: "trusted",
            csrf_token: csrfToken,
            curator: true,
            onboarding_required: false,
          },
        });
      }
      return;
    }
    if (recorded.path === "/api/recovery/review") {
      await route.fulfill({
        json: {
          held: true,
          started_at: "2026-07-30T12:00:00Z",
          counts: {
            people: 12,
            current_recipients: 8,
            completed_recipients: 7,
            suspended_recipients: 1,
            revoked_generations: 2,
            restored_sessions: 9,
            fresh_sessions: 1,
            audience_entitlements: 24,
            published_events: 5,
            published_media_items: 100,
            active_withdrawals: 2,
            pending_email_batches: 3,
            active_push_subscriptions: 4,
          },
        },
      });
      return;
    }
    if (recorded.path === "/api/recovery/review/complete") {
      await route.fulfill({ status: 204 });
      return;
    }
    if (recorded.path === "/api/recovery/release") {
      released = true;
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({
      status: 500,
      json: { error: { message: `Unexpected request: ${recorded.path}` } },
    });
  });

  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Review restored authorization state" }),
  ).toBeVisible();
  await expect(page.getByText("Invalidated restored Sessions")).toBeVisible();
  const release = page.getByRole("button", {
    name: "Release Recovery hold",
  });
  await expect(release).toBeDisabled();
  const reviewed = page.getByLabel(
    /I reviewed the restored Recipient access, Sessions, Audiences/,
  );
  await reviewed.check();
  await expect(reviewed).toBeFocused();
  await expect(release).toBeEnabled();
  await release.click();

  await expect(
    page.getByRole("heading", { name: "Confirming Recovery release" }),
  ).toBeVisible();
  await expect(page.getByRole("status")).toHaveText(
    "Restoring normal access securely…",
  );
  expect(mutations).toHaveLength(2);
  for (const mutation of mutations) {
    expect(mutation).toMatchObject({ method: "POST", body: null });
    expect(mutation.headers["x-memento-csrf"]).toBe(csrfToken);
  }
  expect(mutations.map(({ path }) => path)).toEqual([
    "/api/recovery/review/complete",
    "/api/recovery/release",
  ]);

  allowConfirmation();
  await expect(
    page.getByRole("heading", { name: "Sign in to Memento" }),
  ).toBeVisible();
  await expectNoHorizontalOverflow(page);
});
