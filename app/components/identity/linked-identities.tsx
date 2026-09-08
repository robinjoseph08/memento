import { formatDate } from "../../lib/utils";
import type { LinkedIdentity } from "../../types/generated/identity";
import { ConfirmAction } from "../people/confirm-action";
import { sectionHeadingClass } from "../people/form-fields";

export function LinkedIdentities({
  identities,
  pending,
  error,
  unlink,
}: {
  identities: LinkedIdentity[];
  pending: boolean;
  error: unknown;
  unlink: (id: string) => Promise<unknown>;
}) {
  return (
    <section
      aria-labelledby="linked-accounts"
      className="border-t border-border py-8"
    >
      <h2 className={sectionHeadingClass} id="linked-accounts">
        Linked accounts
      </h2>
      <p className="mt-3 mb-5 max-w-150 text-sm text-muted">
        Unlinking an account signs out all browsers using it. Keep at least one
        linked account to retain sign-in access.
      </p>
      {identities.length ? (
        <ul className="divide-y divide-border">
          {identities.map((identity) => (
            <li
              className="flex flex-wrap items-center justify-between gap-4 py-5"
              key={identity.id}
            >
              <div className="min-w-0">
                <p className="wrap-anywhere">{identity.email}</p>
                <p className="mt-2 text-xs text-muted">
                  {identity.provider === "google" ? "Google" : "Development"}.
                  Linked {formatDate(identity.created_at)}
                </p>
              </div>
              <ConfirmAction
                description="This account will no longer be able to sign in, and its browser sessions will end."
                error={error}
                label={`Unlink ${identity.email}`}
                onConfirm={() => unlink(identity.id)}
                pending={pending}
              />
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted">No accounts linked yet.</p>
      )}
    </section>
  );
}
