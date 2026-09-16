import { Clapperboard, Mail, Plug } from "lucide-react";

import {
  useChapterStatus,
  useEmailStatus,
} from "../../hooks/queries/connection";
import { ConnectionDetails } from "../connection/connection-status";
import { headingClass, SectionHeading } from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { IntegrationStatus, type Verdict } from "./integration-status";

// Installation-level checks live here rather than on the dashboard, which
// only reports an Immich problem when there is one.
export function SettingsPage() {
  return (
    <>
      <PageTitle title="Settings" />
      <h1 className={headingClass}>Settings</h1>
      <div className="max-w-[640px]">
        <section aria-labelledby="immich-connection" className="mt-9">
          <SectionHeading icon={Plug} id="immich-connection">
            Immich connection
          </SectionHeading>
          <ConnectionDetails area="curator" />
        </section>
        <section aria-labelledby="email-status" className="mt-12">
          <SectionHeading icon={Mail} id="email-status">
            Email
          </SectionHeading>
          <EmailDetails />
        </section>
        <section aria-labelledby="chapters-status" className="mt-12">
          <SectionHeading icon={Clapperboard} id="chapters-status">
            Video chapters
          </SectionHeading>
          <ChapterDetails />
        </section>
      </div>
    </>
  );
}

// Email is optional, so a missing mail server is a quiet state rather than
// a failure. A check connects and signs in without sending anything.
function EmailDetails() {
  const email = useEmailStatus();
  const verdict: Verdict | undefined = email.data && {
    tone: !email.data.configured
      ? "unconfigured"
      : email.data.usable
        ? "usable"
        : "unusable",
    label: !email.data.configured
      ? "Not configured"
      : email.data.usable
        ? "Connected"
        : "Not connected",
    message: email.data.message,
    detail: email.data.sender ? `Sending as ${email.data.sender}` : undefined,
  };
  return (
    <IntegrationStatus
      checking="Checking email…"
      query={email}
      verdict={verdict}
    >
      The mail server and sender address are configured on the server. Passwords
      are never shown here. Checking connects to the mail server without sending
      anything.
    </IntegrationStatus>
  );
}

function ChapterDetails() {
  const chapters = useChapterStatus();
  const verdict: Verdict | undefined = chapters.data && {
    tone: chapters.data.usable ? "usable" : "unusable",
    label: chapters.data.usable ? "Ready" : "Not ready",
    message: chapters.data.message,
    detail: chapters.data.version
      ? `ffprobe ${chapters.data.version}`
      : undefined,
  };
  return (
    <IntegrationStatus
      checking="Checking video chapters…"
      query={chapters}
      verdict={verdict}
    >
      Memento reads chapter markers from videos with ffprobe, which ships inside
      the server image.
    </IntegrationStatus>
  );
}
