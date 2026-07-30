import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";

import { apiJSON, apiNoContent } from "./api";
import type {
  CuratorActivityItem,
  CuratorActivityResponse,
  CuratorWorkItem,
  CuratorWorkResponse,
  MarkReadRequest,
} from "./types/generated/activity";
import type { SessionResponse } from "./types/generated/setup";

const categories = [
  "",
  "security",
  "access",
  "publication",
  "withdrawal",
  "comment",
  "favorite",
  "invitation_suggestion",
  "delivery",
] as const;

function formatWhen(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "Unknown time"
    : new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

function label(value: string) {
  return value.replaceAll("_", " ");
}

function attribution(item: CuratorActivityItem) {
  if (
    item.actor &&
    item.subject &&
    item.actor.person_id !== item.subject.person_id
  ) {
    return `${item.actor.person_name} for ${item.subject.person_name}`;
  }
  return item.actor?.person_name ?? item.subject?.person_name ?? "Memento";
}

export function CuratorActivity({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab =
    searchParams.get("activity_view") === "activity" ? "activity" : "work";
  const requestedCategory = searchParams.get("activity_category") ?? "";
  const category = categories.includes(
    requestedCategory as (typeof categories)[number],
  )
    ? requestedCategory
    : "";
  const unreadOnly = searchParams.get("activity_unread") === "true";
  const work = useInfiniteQuery({
    queryKey: ["curator-work"],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<CuratorWorkResponse>(
        `/api/activity/curator/work?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: tab === "work",
  });
  const activity = useInfiniteQuery({
    queryKey: ["curator-activity", category, unreadOnly],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (category) params.set("category", category);
      if (unreadOnly) params.set("unread", "true");
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<CuratorActivityResponse>(
        `/api/activity/curator?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: tab === "activity",
  });
  const markRead = useMutation({
    mutationFn: (request: MarkReadRequest) =>
      apiNoContent("/api/activity/curator/read", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSettled: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["curator-work"] }),
        queryClient.invalidateQueries({ queryKey: ["curator-activity"] }),
      ]);
    },
  });

  function setActivityView(nextTab: "work" | "activity") {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (nextTab === "activity") next.set("activity_view", "activity");
      else next.delete("activity_view");
      return next;
    });
  }

  function setActivityCategory(nextCategory: string) {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (nextCategory) next.set("activity_category", nextCategory);
      else next.delete("activity_category");
      return next;
    });
  }

  function setActivityUnread(nextUnread: boolean) {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (nextUnread) next.set("activity_unread", "true");
      else next.delete("activity_unread");
      return next;
    });
  }

  function markItemRead(
    item: CuratorWorkItem | CuratorActivityItem,
    surface: "work" | "activity",
  ) {
    if (item.read) return;
    markRead.mutate({
      surface,
      source_kind: item.source_kind,
      source_id: item.source_id,
      version: item.version,
    });
  }

  function openWork(item: CuratorWorkItem) {
    markItemRead(item, "work");
    if (item.source_kind === "event") {
      setSearchParams((current) => {
        const next = new URLSearchParams(current);
        next.set("workspace", "drafts");
        next.set("event", item.source_id);
        return next;
      });
      return;
    }
    const target =
      item.source_kind === "source_album"
        ? "curator-sources"
        : item.source_kind === "source_problem" ||
            item.source_kind === "media_problem"
          ? "curator-repairs"
          : "curator-people";
    document.getElementById(target)?.scrollIntoView({ behavior: "smooth" });
  }

  return (
    <section
      aria-labelledby="curator-activity-title"
      className="shell-card curator-activity"
    >
      <header className="curator-activity-heading">
        <div>
          <p className="eyebrow">MEMENTO CURATOR</p>
          <h2 id="curator-activity-title">Work and activity</h2>
        </div>
        <div
          aria-label="Curator activity view"
          className="activity-tabs"
          role="tablist"
        >
          <button
            aria-controls="curator-work-panel"
            aria-selected={tab === "work"}
            id="curator-work-tab"
            onClick={() => setActivityView("work")}
            onKeyDown={(event) => {
              if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
                event.preventDefault();
                setActivityView("activity");
                document.getElementById("curator-activity-tab")?.focus();
              }
            }}
            role="tab"
            tabIndex={tab === "work" ? 0 : -1}
            type="button"
          >
            Work
          </button>
          <button
            aria-controls="curator-activity-panel"
            aria-selected={tab === "activity"}
            id="curator-activity-tab"
            onClick={() => setActivityView("activity")}
            onKeyDown={(event) => {
              if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
                event.preventDefault();
                setActivityView("work");
                document.getElementById("curator-work-tab")?.focus();
              }
            }}
            role="tab"
            tabIndex={tab === "activity" ? 0 : -1}
            type="button"
          >
            Activity
          </button>
        </div>
      </header>

      {tab === "work" ? (
        <div
          aria-labelledby="curator-work-tab"
          className="curator-work-list"
          id="curator-work-panel"
          role="tabpanel"
        >
          {work.isPending ? <p>Loading prioritized work…</p> : null}
          {work.error ? (
            <p className="form-error" role="alert">
              {work.error.message}
            </p>
          ) : null}
          {work.data?.pages
            .flatMap((page) => page.items)
            .map((item) => (
              <article
                className={item.read ? "work-item" : "work-item unread"}
                key={item.id}
              >
                <div>
                  <p className="eyebrow">{label(item.kind)}</p>
                  <h3>{item.title}</h3>
                  <p>{item.summary}</p>
                  {item.completed_steps.length ||
                  item.remaining_steps.length ? (
                    <p className="work-progress">
                      {item.completed_steps.length} complete ·{" "}
                      {item.remaining_steps.length} remaining
                    </p>
                  ) : null}
                </div>
                <button onClick={() => openWork(item)} type="button">
                  {item.next_action_label}
                </button>
              </article>
            ))}
          {work.hasNextPage ? (
            <button
              disabled={work.isFetchingNextPage}
              onClick={() => void work.fetchNextPage()}
              type="button"
            >
              {work.isFetchingNextPage ? "Loading…" : "Load more work"}
            </button>
          ) : null}
          {work.isSuccess &&
          work.data.pages.every((page) => page.items.length === 0) ? (
            <p>No Curator work is waiting.</p>
          ) : null}
        </div>
      ) : (
        <div
          aria-labelledby="curator-activity-tab"
          id="curator-activity-panel"
          role="tabpanel"
        >
          <div className="activity-filters">
            <label>
              Category
              <select
                onChange={(event) => setActivityCategory(event.target.value)}
                value={category}
              >
                {categories.map((item) => (
                  <option key={item || "all"} value={item}>
                    {item ? label(item) : "All activity"}
                  </option>
                ))}
              </select>
            </label>
            <label className="inline-choice">
              <input
                checked={unreadOnly}
                onChange={(event) => setActivityUnread(event.target.checked)}
                type="checkbox"
              />
              Unread only
            </label>
          </div>
          {activity.isPending ? <p>Loading chronological activity…</p> : null}
          {activity.error ? (
            <p className="form-error" role="alert">
              {activity.error.message}
            </p>
          ) : null}
          <ol className="activity-list">
            {activity.data?.pages
              .flatMap((page) => page.items)
              .map((item) => (
                <li
                  className={
                    item.read ? "activity-item" : "activity-item unread"
                  }
                  key={item.id}
                >
                  <button
                    aria-label={`Mark ${label(item.action)} read`}
                    disabled={item.read || markRead.isPending}
                    onClick={() => markItemRead(item, "activity")}
                    type="button"
                  >
                    <strong>{label(item.action)}</strong>
                    <span>{attribution(item)}</span>
                    {item.target_label ? (
                      <span>{item.target_label}</span>
                    ) : null}
                    <time dateTime={item.created_at}>
                      {formatWhen(item.created_at)}
                    </time>
                  </button>
                </li>
              ))}
          </ol>
          {activity.hasNextPage ? (
            <button
              disabled={activity.isFetchingNextPage}
              onClick={() => void activity.fetchNextPage()}
              type="button"
            >
              {activity.isFetchingNextPage ? "Loading…" : "Load older activity"}
            </button>
          ) : null}
          {activity.isSuccess &&
          activity.data.pages.every((page) => page.items.length === 0) ? (
            <p>No matching activity.</p>
          ) : null}
        </div>
      )}
    </section>
  );
}
