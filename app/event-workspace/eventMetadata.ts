import type { Event } from "../types/generated/events";

export function validateEventMetadata(event: Event | undefined) {
  const titleError =
    event && event.title.trim() === "" ? "Event title is required." : "";
  let dateError = "";
  let coverError = "";
  let timezoneError = "";
  if (event) {
    if (Boolean(event.date_start) !== Boolean(event.date_end))
      dateError = "Enter both Event dates or clear both dates.";
    else if (
      event.date_start &&
      event.date_end &&
      event.date_start > event.date_end
    )
      dateError = "Event start date must be on or before the end date.";
    const assignedMedia = new Set(
      event.moments.flatMap((moment) =>
        moment.media_items.map((item) => item.id),
      ),
    );
    if (
      event.selected_cover_media_item_id &&
      !assignedMedia.has(event.selected_cover_media_item_id)
    )
      coverError = "Choose Event cover Media assigned to a Moment.";
    const timezone = event.grouping_timezone.trim();
    try {
      if (!timezone || timezone === "Local") throw new Error("invalid");
      new Intl.DateTimeFormat("en-US", { timeZone: timezone }).format();
    } catch {
      timezoneError =
        "Enter a valid IANA timezone, such as America/New_York or UTC.";
    }
  }
  return {
    coverError,
    dateError,
    titleError,
    timezoneError,
    valid: !coverError && !dateError && !titleError && !timezoneError,
  };
}
