import { Link } from "react-router-dom";

import { useRetryAlbum } from "../../hooks/queries/albums";
import { useConnection } from "../../hooks/queries/connection";
import { useDashboard } from "../../hooks/queries/dashboard";
import { useIdentityStatus } from "../../hooks/queries/identity";
import { errorMessage } from "../../lib/http";
import type {
  AlbumWork,
  ChapterWork,
  Dashboard,
  Delivery,
} from "../../types/generated/dashboard";
import { importLabels } from "../albums/album-state";
import { countLabel } from "../albums/moment-labels";
import { DeliveryStatus } from "../notifications/delivery-status";
import {
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";

const linkClass =
  "inline-flex min-h-9 items-center rounded-md px-2 -mx-2 text-sm text-accent-foreground underline underline-offset-4 hover:bg-surface";

// The Curator's home: what needs a decision, what can wait, and the three
// things Curators do most. No counts for their own sake.
export function DashboardPage() {
  const { data: identity } = useIdentityStatus();
  const dashboard = useDashboard();
  return (
    <>
      <PageTitle title="Home" />
      <h1 className={headingClass}>
        {identity?.person
          ? `Hi, ${identity.person.display_name.split(" ")[0]}`
          : "Home"}
      </h1>
      <p className="mt-5 max-w-[640px] text-muted">
        What needs a decision, and what can wait until you are ready.
      </p>
      <div className="mt-7 flex flex-wrap gap-3">
        <Button asChild>
          <Link to="/curator/import">Import an album</Link>
        </Button>
        <Button asChild variant="outline">
          <Link to="/curator/updates">Send updates</Link>
        </Button>
        <Button asChild variant="outline">
          <Link to="/curator/people">Manage people</Link>
        </Button>
      </div>
      {dashboard.isPending && (
        <p className="mt-9" role="status">
          Checking what needs you…
        </p>
      )}
      {dashboard.isError && !dashboard.data && (
        <div className="mt-9">
          <ReadFailure
            error={dashboard.error}
            pending={dashboard.isFetching}
            retry={dashboard.refetch}
          />
        </div>
      )}
      {dashboard.data && (
        <>
          {dashboard.data.active && (
            <p className="mt-9 text-sm text-muted" role="status">
              Memento is still importing or sending email. This page updates on
              its own.
            </p>
          )}
          <NeedsAttention dashboard={dashboard.data} />
          <ReadyWhenYouAre ready={dashboard.data.ready} />
        </>
      )}
    </>
  );
}

function NeedsAttention({ dashboard }: { dashboard: Dashboard }) {
  const connection = useConnection("curator", true);
  const { needs_attention: attention } = dashboard;
  const immichProblem =
    (connection.data && !connection.data.usable) || connection.isError;
  // The Immich check is a network call that may take a while to fail, so the
  // group is not declared empty until it has answered.
  const empty =
    !immichProblem &&
    !connection.isPending &&
    attention.pending_requests === 0 &&
    attention.imports.length === 0 &&
    attention.deliveries.length === 0 &&
    attention.chapters.length === 0;
  return (
    <section aria-labelledby="needs-attention" className="mt-9">
      <h2 className={sectionHeadingClass} id="needs-attention">
        Needs attention
      </h2>
      {empty ? (
        <p className="mt-4 text-muted">
          Nothing needs your attention right now.
        </p>
      ) : (
        <ul className="mt-5 divide-y divide-border border-y border-border">
          {connection.isPending && (
            <li className="py-4 text-sm text-muted" role="status">
              Checking the Immich connection…
            </li>
          )}
          {immichProblem && (
            <Item
              action={
                <Link className={linkClass} to="/curator/settings">
                  Check the connection
                </Link>
              }
              detail={
                connection.isError
                  ? errorMessage(connection.error)
                  : connection.data?.message
              }
              title="Immich is not connected"
            />
          )}
          {attention.pending_requests > 0 && (
            <Item
              action={
                <Link className={linkClass} to="/curator/requests">
                  Review requests
                </Link>
              }
              detail="People are waiting to find out whether they can see anything."
              title={`${countLabel(attention.pending_requests, "access request", "access requests")} waiting for a decision`}
            />
          )}
          {attention.imports.map((album) => (
            <ImportItem album={album} key={album.id} />
          ))}
          {attention.deliveries.map((delivery) => (
            <DeliveryItem delivery={delivery} key={delivery.id} />
          ))}
          {attention.chapters.map((chapter) => (
            <ChapterItem chapter={chapter} key={chapter.entry_id} />
          ))}
        </ul>
      )}
    </section>
  );
}

function ReadyWhenYouAre({ ready }: { ready: Dashboard["ready"] }) {
  const empty =
    ready.unpublished.length === 0 && ready.unannounced_people === 0;
  return (
    <section aria-labelledby="ready-when-you-are" className="mt-12">
      <h2 className={sectionHeadingClass} id="ready-when-you-are">
        Ready when you are
      </h2>
      {empty ? (
        <p className="mt-4 text-muted">
          Everything is published and everyone has heard about it.
        </p>
      ) : (
        <ul className="mt-5 divide-y divide-border border-y border-border">
          {ready.unannounced_people > 0 && (
            <Item
              action={
                <Link className={linkClass} to="/curator/updates">
                  Send updates
                </Link>
              }
              detail="They can already see the photos; this only lets them know."
              title={`${countLabel(ready.unannounced_people, "person has", "people have")} new photos or videos to hear about`}
            />
          )}
          {ready.unpublished.map((album) => (
            <Item
              action={
                <Link
                  className={linkClass}
                  to={
                    album.ready
                      ? `/curator/albums/${album.id}`
                      : `/curator/albums/${album.id}?section=access`
                  }
                >
                  {album.ready ? "Review and publish" : "Set up access"}
                </Link>
              }
              detail={
                album.ready
                  ? "Not published yet. Publish whenever you like."
                  : "Not published yet. Nobody has access yet."
              }
              key={album.id}
              title={album.title}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function Item({
  title,
  detail,
  action,
  children,
}: {
  title: string;
  detail?: string;
  action?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <li className="flex flex-wrap items-start justify-between gap-x-8 gap-y-3 py-4">
      <div className="max-w-[640px] min-w-0">
        <p className="font-medium wrap-anywhere">{title}</p>
        {detail && <p className="mt-1 text-sm text-muted">{detail}</p>}
        {children}
      </div>
      {action && (
        <div className="flex flex-wrap items-center gap-2">{action}</div>
      )}
    </li>
  );
}

function ImportItem({ album }: { album: AlbumWork }) {
  const retry = useRetryAlbum(album.id);
  return (
    <Item
      action={
        <>
          <Form
            aria-busy={retry.isPending}
            aria-label={`Retry import of ${album.title}`}
            error={retry.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (!retry.isPending) retry.mutate();
            }}
          >
            <Button
              disabled={retry.isPending}
              size="sm"
              type="submit"
              variant="outline"
            >
              {retry.isPending ? "Retrying…" : "Retry import"}
            </Button>
          </Form>
          <Link className={linkClass} to={`/curator/albums/${album.id}`}>
            Open album
          </Link>
        </>
      }
      detail={album.message}
      title={`${album.title}: ${importLabels[album.status] ?? "Import needs attention"}`}
    />
  );
}

const kindLabels: Record<string, string> = {
  update: "Update email",
  invitation: "Invitation",
  access_request: "Access request alert",
};

function DeliveryItem({ delivery }: { delivery: Delivery }) {
  const title = delivery.person_name
    ? `${kindLabels[delivery.kind] ?? "Email"} for ${delivery.person_name}`
    : `${kindLabels[delivery.kind] ?? "Email"} to ${delivery.recipient}`;
  return (
    <Item
      action={
        delivery.kind === "invitation" && delivery.person_id ? (
          <Link
            className={linkClass}
            to={`/curator/people/${delivery.person_id}`}
          >
            Open {delivery.person_name || "person"}
          </Link>
        ) : undefined
      }
      title={title}
    >
      <DeliveryStatus
        className="mt-1"
        delivery={delivery}
        recipient={delivery.recipient}
        retry={delivery.kind === "invitation" ? false : undefined}
      />
    </Item>
  );
}

function ChapterItem({ chapter }: { chapter: ChapterWork }) {
  return (
    <Item
      action={
        <Link
          className={linkClass}
          to={`/curator/albums/${chapter.album_id}?moment=${encodeURIComponent(chapter.moment_id)}&entry=${encodeURIComponent(chapter.entry_id)}&pane=detail`}
        >
          Open video
        </Link>
      }
      detail={`${chapter.message} Playback still works.`}
      title={`Chapters for ${chapter.title} in ${chapter.album_title}`}
    />
  );
}
