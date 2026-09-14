import { ConnectionDetails } from "../connection/connection-status";
import { headingClass, sectionHeadingClass } from "../people/form-fields";
import { PageTitle } from "../shell/page-title";

// Installation-level checks live here rather than on the dashboard, which
// only reports an Immich problem when there is one.
export function SettingsPage() {
  return (
    <>
      <PageTitle title="Settings" />
      <h1 className={headingClass}>Settings</h1>
      <section
        aria-labelledby="immich-connection"
        className="mt-9 max-w-[640px]"
      >
        <h2 className={sectionHeadingClass} id="immich-connection">
          Immich connection
        </h2>
        <ConnectionDetails area="curator" />
      </section>
    </>
  );
}
