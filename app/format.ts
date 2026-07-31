export function formatSourceDate(value: unknown) {
  if (typeof value !== "string") return "Unknown";
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "Unknown"
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(date);
}

export function formatInvitationExpiry(value: unknown) {
  if (typeof value !== "string") return "an unknown time";
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "an unknown time"
    : new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "long",
      }).format(date);
}
