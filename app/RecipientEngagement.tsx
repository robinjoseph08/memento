import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiJSON } from "./api";
import type {
  MediaOpenersResponse,
  RecipientDetail,
} from "./types/generated/engagement";

function formatWhen(value: string | null) {
  if (!value) return "No meaningful authenticated activity yet";
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

export function RecipientEngagement({ personID }: { personID: string }) {
  const [openersMediaID, setOpenersMediaID] = useState<string>();
  const engagement = useInfiniteQuery({
    queryKey: ["recipient-engagement", personID],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<RecipientDetail>(
        `/api/engagement/recipients/${personID}?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  });
  const mediaOpeners = useQuery({
    queryKey: ["media-engagement-openers", openersMediaID],
    queryFn: () =>
      apiJSON<MediaOpenersResponse>(
        `/api/engagement/media/${openersMediaID}/openers`,
      ),
    enabled: Boolean(openersMediaID),
    retry: false,
  });

  if (engagement.isPending) return <p>Loading meaningful activity…</p>;
  if (engagement.error) {
    return (
      <p className="form-error" role="alert">
        {engagement.error.message}
      </p>
    );
  }
  const detail = engagement.data.pages[0];
  const timeline = engagement.data.pages.flatMap((page) => page.timeline);
  return (
    <section
      aria-labelledby={`engagement-${personID}`}
      className="recipient-engagement"
    >
      <h4 id={`engagement-${personID}`}>Meaningful authenticated activity</h4>
      <p className="engagement-latest">
        <strong>Latest activity</strong>{" "}
        {formatWhen(detail.latest_meaningful_activity_at)}
      </p>
      <dl className="engagement-metrics">
        <div>
          <dt>Active days</dt>
          <dd>
            {detail.active_days.days_7} in 7 days · {detail.active_days.days_30}{" "}
            in 30 · {detail.active_days.days_90} in 90
          </dd>
        </div>
        <div>
          <dt>Visit frequency</dt>
          <dd>
            {detail.visit_frequency.visits_30_days} visits across{" "}
            {detail.visit_frequency.active_visit_days_30} active days in 30 days
          </dd>
        </div>
        <div>
          <dt>Explicit Event opens</dt>
          <dd>{detail.counts_90_days.event_opens}</dd>
        </div>
        <div>
          <dt>Explicit Media opens</dt>
          <dd>{detail.counts_90_days.media_opens}</dd>
        </div>
        <div>
          <dt>Downloads</dt>
          <dd>{detail.counts_90_days.downloads}</dd>
        </div>
        <div>
          <dt>Comments</dt>
          <dd>{detail.counts_90_days.comments}</dd>
        </div>
        <div>
          <dt>Favorite changes</dt>
          <dd>{detail.counts_90_days.favorite_changes}</dd>
        </div>
        <div>
          <dt>Invitation suggestions</dt>
          <dd>{detail.counts_90_days.invitation_suggestions}</dd>
        </div>
      </dl>
      <p>
        Only explicit opens are counted. Thumbnail display, prefetching,
        service-worker traffic, delivery, email opens, and Curator preview are
        excluded.
      </p>
      <details>
        <summary>Recent engagement timeline</summary>
        {timeline.length ? (
          <ol className="engagement-timeline">
            {timeline.map((item) => (
              <li key={item.id}>
                <strong>{label(item.kind)}</strong>
                {item.target_kind === "media" && item.target_id
                  ? ` · Media ${item.target_id}`
                  : item.target_label
                    ? ` · ${item.target_label}`
                    : ""}
                {` · ${formatWhen(item.occurred_at)}`}
                {item.kind === "media_opened" && item.target_id ? (
                  <button
                    onClick={() =>
                      setOpenersMediaID(item.target_id ?? undefined)
                    }
                    type="button"
                  >
                    Inspect Media openers
                  </button>
                ) : null}
              </li>
            ))}
          </ol>
        ) : (
          <p>No detailed engagement in the retained year.</p>
        )}
        {openersMediaID ? (
          <section aria-live="polite" className="media-openers">
            <h5>Recipients who explicitly opened Media {openersMediaID}</h5>
            {mediaOpeners.isPending ? <p>Loading Media openers…</p> : null}
            {mediaOpeners.error ? (
              <p className="form-error" role="alert">
                {mediaOpeners.error.message}
              </p>
            ) : null}
            {mediaOpeners.data?.openers.length ? (
              <ul>
                {mediaOpeners.data.openers.map((opener) => (
                  <li key={opener.recipient_person_id}>
                    {opener.recipient_name}: {opener.open_count} explicit opens,
                    latest {formatWhen(opener.latest_opened_at)}
                  </li>
                ))}
              </ul>
            ) : mediaOpeners.isSuccess ? (
              <p>No retained explicit Media opens.</p>
            ) : null}
          </section>
        ) : null}
        {engagement.hasNextPage ? (
          <button
            disabled={engagement.isFetchingNextPage}
            onClick={() => void engagement.fetchNextPage()}
            type="button"
          >
            {engagement.isFetchingNextPage
              ? "Loading…"
              : "Load older engagement"}
          </button>
        ) : null}
      </details>
    </section>
  );
}
