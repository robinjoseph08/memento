import type { Event } from "../types/generated/events";

export function validateEventMetadata(event: Event | undefined) {
  const titleError =
    event && event.title.trim() === "" ? "Event title is required." : "";
  let timezoneError = "";
  if (event) {
    const timezone = event.grouping_timezone.trim();
    try {
      if (!timezone || timezone === "Local") throw new Error("invalid");
      new Intl.DateTimeFormat("en-US", { timeZone: timezone }).format();
    } catch {
      timezoneError =
        "Enter a valid IANA timezone, such as America/New_York or UTC.";
    }
  }
  return { titleError, timezoneError, valid: !titleError && !timezoneError };
}
