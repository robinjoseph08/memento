import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useState } from "react";
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
  const [, setSearchParams] = useSearchParams();
  const [tab, setTab] = useState<"work" | "activity">("work");
  const [category, setCategory] = useState("");
  const [unreadOnly, setUnreadOnly] = useState(false);
  const work = useQuery({
    queryKey: ["curator-work"],
    queryFn: () => apiJSON<CuratorWorkResponse>("/api/activity/curator/work"),
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

  function read(
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
    read(item, "work");
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
            aria-selected={tab === "work"}
            onClick={() => setTab("work")}
            role="tab"
            type="button"
          >
            Work
          </button>
          <button
            aria-selected={tab === "activity"}
            onClick={() => setTab("activity")}
            role="tab"
            type="button"
          >
            Activity
          </button>
        </div>
      </header>

      {tab === "work" ? (
        <div className="curator-work-list">
          {work.isPending ? <p>Loading prioritized work…</p> : null}
          {work.error ? (
            <p className="form-error" role="alert">
              {work.error.message}
            </p>
          ) : null}
          {work.data?.items.map((item) => (
            <article
              className={item.read ? "work-item" : "work-item unread"}
              key={item.id}
            >
              <div>
                <p className="eyebrow">{label(item.kind)}</p>
                <h3>{item.title}</h3>
                <p>{item.summary}</p>
                {item.completed_steps.length || item.remaining_steps.length ? (
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
          {work.isSuccess && work.data.items.length === 0 ? (
            <p>No Curator work is waiting.</p>
          ) : null}
        </div>
      ) : (
        <div>
          <div className="activity-filters">
            <label>
              Category
              <select
                onChange={(event) => setCategory(event.target.value)}
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
                onChange={(event) => setUnreadOnly(event.target.checked)}
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
                    onClick={() => read(item, "activity")}
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
