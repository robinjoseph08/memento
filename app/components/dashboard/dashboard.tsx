import {
  BellRing,
  Film,
  Image,
  MailWarning,
  ServerOff,
  TriangleAlert,
  UserPlus,
  type LucideIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";

import { useRetryAlbum } from "../../hooks/queries/albums";
import { useConnection } from "../../hooks/queries/connection";
import { useDashboard } from "../../hooks/queries/dashboard";
import { useIdentityStatus } from "../../hooks/queries/identity";
import { errorMessage } from "../../lib/http";
import { cn } from "../../lib/utils";
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
        <p className="mt-10" role="status">
          Checking what needs you…
        </p>
      )}
      {dashboard.isError && !dashboard.data && (
        <div className="mt-10">
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
            <p className="mt-10 text-sm text-muted" role="status">
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

// Section is one of the two groups: a heading with the number of items, one
// line saying what belongs here, and either the cards or a quiet empty state.
function Section({
  id,
  title,
  description,
  count,
  tone,
  empty,
  children,
}: {
  id: string;
  title: string;
  description: string;
  count: number;
  tone: "attention" | "ready";
  empty: string;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="mt-12">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h2 className={sectionHeadingClass} id={id}>
          {title}
        </h2>
        {count > 0 && (
          <span
            className={cn(
              "rounded-full px-2.5 py-0.5 text-xs font-medium",
              tone === "attention"
                ? "bg-destructive/15 text-destructive"
                : "bg-accent text-accent-foreground",
            )}
          >
            {count}
            <span className="sr-only">{count === 1 ? " item" : " items"}</span>
          </span>
        )}
      </div>
      <p className="mt-2 text-sm text-muted">{description}</p>
      {count === 0 ? (
        <p className="mt-5 rounded-lg border border-dashed border-border px-5 py-6 text-sm text-muted">
          {empty}
        </p>
      ) : (
        <ul className="mt-5 flex flex-col gap-3">{children}</ul>
      )}
    </section>
  );
}

// Item is one card: an icon that says what kind of work it is, a title, one
// line of detail, and the action that moves it along on the right.
function Item({
  icon: Icon,
  tone,
  title,
  detail,
  action,
  children,
}: {
  icon: LucideIcon;
  tone: "attention" | "ready" | "pending";
  title: string;
  detail?: string;
  action?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <li
      className={cn(
        "flex flex-wrap items-start gap-x-5 gap-y-4 rounded-lg border border-border bg-surface p-4 min-[601px]:p-5",
        tone === "attention" && "border-l-4 border-l-destructive",
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn(
          "mt-0.5 size-5 shrink-0",
          tone === "attention" ? "text-destructive" : "text-muted",
        )}
        strokeWidth={1.5}
      />
      <div className="min-w-0 flex-1 basis-60">
        <p className="text-base font-medium wrap-anywhere">{title}</p>
        {detail && (
          <p className="mt-1 text-sm wrap-anywhere text-muted">{detail}</p>
        )}
        {children}
      </div>
      {action && (
        <div className="flex flex-wrap items-center gap-2 min-[601px]:self-center">
          {action}
        </div>
      )}
    </li>
  );
}

function ActionLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Button asChild size="sm" variant="outline">
      <Link to={to}>{children}</Link>
    </Button>
  );
}

function NeedsAttention({ dashboard }: { dashboard: Dashboard }) {
  const connection = useConnection("curator", true);
  const { needs_attention: attention } = dashboard;
  const immichProblem =
    (connection.data && !connection.data.usable) || connection.isError;
  // The Immich check is a network call that may take a while to fail, so the
  // group is not declared empty until it has answered.
  const count =
    (immichProblem || connection.isPending ? 1 : 0) +
    (attention.pending_requests > 0 ? 1 : 0) +
    attention.imports.length +
    attention.deliveries.length +
    attention.chapters.length;
  return (
    <Section
      count={count}
      description="Failures and decisions that will not resolve on their own."
      empty="Nothing needs your attention right now."
      id="needs-attention"
      title="Needs attention"
      tone="attention"
    >
      {connection.isPending && (
        <li
          className="rounded-lg border border-dashed border-border px-5 py-4 text-sm text-muted"
          role="status"
        >
          Checking the Immich connection…
        </li>
      )}
      {immichProblem && (
        <Item
          action={
            <ActionLink to="/curator/settings">Check the connection</ActionLink>
          }
          detail={
            connection.isError
              ? errorMessage(connection.error)
              : connection.data?.message
          }
          icon={ServerOff}
          title="Immich is not connected"
          tone="attention"
        />
      )}
      {attention.pending_requests > 0 && (
        <Item
          action={
            <ActionLink to="/curator/requests">Review requests</ActionLink>
          }
          detail="People are waiting to find out whether they can see anything."
          icon={UserPlus}
          title={`${countLabel(attention.pending_requests, "access request", "access requests")} waiting for a decision`}
          tone="attention"
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
    </Section>
  );
}

function ReadyWhenYouAre({ ready }: { ready: Dashboard["ready"] }) {
  const count =
    ready.unpublished.length + (ready.unannounced_people > 0 ? 1 : 0);
  return (
    <Section
      count={count}
      description="Nothing is wrong here. Finish these whenever you like."
      empty="Everything is published and everyone has heard about it."
      id="ready-when-you-are"
      title="Ready when you are"
      tone="ready"
    >
      {ready.unannounced_people > 0 && (
        <Item
          action={<ActionLink to="/curator/updates">Send updates</ActionLink>}
          detail="They can already see the photos; this only lets them know."
          icon={BellRing}
          title={`${countLabel(ready.unannounced_people, "person has", "people have")} new photos or videos to hear about`}
          tone="ready"
        />
      )}
      {ready.unpublished.map((album) => (
        <Item
          action={
            <ActionLink
              to={
                album.ready
                  ? `/curator/albums/${album.id}`
                  : `/curator/albums/${album.id}?section=access`
              }
            >
              {album.ready ? "Review and publish" : "Set up access"}
            </ActionLink>
          }
          detail={
            album.ready
              ? "Not published yet. Publish whenever you like."
              : "Not published yet. Nobody has access yet."
          }
          icon={Image}
          key={album.id}
          title={album.title}
          tone="ready"
        />
      ))}
    </Section>
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
            <Button disabled={retry.isPending} size="sm" type="submit">
              {retry.isPending ? "Retrying…" : "Retry import"}
            </Button>
          </Form>
          <ActionLink to={`/curator/albums/${album.id}`}>Open album</ActionLink>
        </>
      }
      detail={album.message}
      icon={TriangleAlert}
      title={`${album.title}: ${importLabels[album.status] ?? "Import needs attention"}`}
      tone="attention"
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
          <ActionLink to={`/curator/people/${delivery.person_id}`}>
            Open {delivery.person_name || "person"}
          </ActionLink>
        ) : undefined
      }
      icon={MailWarning}
      title={title}
      tone="attention"
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
        <ActionLink
          to={`/curator/albums/${chapter.album_id}?moment=${encodeURIComponent(chapter.moment_id)}&entry=${encodeURIComponent(chapter.entry_id)}&pane=detail`}
        >
          Open video
        </ActionLink>
      }
      detail={`${chapter.message} Playback still works.`}
      icon={Film}
      title={`Chapters for ${chapter.title} in ${chapter.album_title}`}
      tone="attention"
    />
  );
}
