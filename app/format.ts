function parseDateOnly(value: unknown) {
  if (typeof value !== "string") return undefined;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return undefined;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(Date.UTC(year, month - 1, day));
  if (
    date.getUTCFullYear() !== year ||
    date.getUTCMonth() !== month - 1 ||
    date.getUTCDate() !== day
  )
    return undefined;
  return date;
}

export function formatDateOnly(value: unknown) {
  const date = parseDateOnly(value);
  return date
    ? new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeZone: "UTC",
      }).format(date)
    : "Unknown date";
}

export function formatDateRange(
  dateStart: string | null,
  dateEnd: string | null,
) {
  if (!dateStart || !dateEnd) return "No date range";
  const start = formatDateOnly(dateStart);
  const end = formatDateOnly(dateEnd);
  if (start === "Unknown date" || end === "Unknown date")
    return "Unknown date range";
  return dateStart === dateEnd ? start : `${start} to ${end}`;
}

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
