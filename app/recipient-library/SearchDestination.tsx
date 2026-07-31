import { EventGallery, MediaGallery } from "./MediaGallery";
import { LibraryError } from "./presentation";
import type { OpenedEvent, OpenMedia } from "./types";
import {
  searchDateKind,
  type SearchDestinationModel,
} from "./useSearchDestinationModel";

export function SearchDestination({
  model,
  onOpenEvent,
  onOpenMedia,
}: {
  model: SearchDestinationModel;
  onOpenEvent: (event: OpenedEvent) => void;
  onOpenMedia: OpenMedia;
}) {
  const { search } = model;

  return (
    <div className="recipient-search">
      <form className="search-form" onSubmit={model.submitSearch}>
        <label>
          Search published Events, Place labels, and People
          <input
            autoComplete="off"
            disabled={search.isFetching}
            maxLength={200}
            onChange={(event) => model.setSearchText(event.target.value)}
            placeholder="Family picnic"
            type="search"
            value={model.searchText}
          />
        </label>
        <label>
          Date filter
          <select
            disabled={search.isFetching}
            onChange={(event) =>
              model.setDateKind(searchDateKind(event.target.value))
            }
            value={model.dateKind}
          >
            <option value="">No date filter</option>
            <option value="year">Year</option>
            <option value="month">Month</option>
            <option value="date">Exact date</option>
            <option value="range">Date range</option>
          </select>
        </label>
        {model.dateKind === "year" ? (
          <label>
            Year
            <input
              disabled={search.isFetching}
              max={9999}
              min={1}
              onChange={(event) => model.setSearchYear(event.target.value)}
              required
              type="number"
              value={model.searchYear}
            />
          </label>
        ) : null}
        {model.dateKind === "month" ? (
          <label>
            Month
            <input
              disabled={search.isFetching}
              onChange={(event) => model.setSearchMonth(event.target.value)}
              required
              type="month"
              value={model.searchMonth}
            />
          </label>
        ) : null}
        {model.dateKind === "date" ? (
          <label>
            Date
            <input
              disabled={search.isFetching}
              onChange={(event) => model.setSearchDate(event.target.value)}
              required
              type="date"
              value={model.searchDate}
            />
          </label>
        ) : null}
        {model.dateKind === "range" ? (
          <>
            <label>
              Start date
              <input
                disabled={search.isFetching}
                onChange={(event) => model.setSearchStart(event.target.value)}
                required
                type="date"
                value={model.searchStart}
              />
            </label>
            <label>
              End date
              <input
                disabled={search.isFetching}
                min={model.searchStart}
                onChange={(event) => model.setSearchEnd(event.target.value)}
                required
                type="date"
                value={model.searchEnd}
              />
            </label>
          </>
        ) : null}
        <button
          aria-label="Run search"
          disabled={
            search.isFetching || (!model.searchText.trim() && !model.dateKind)
          }
          type="submit"
        >
          {search.isFetching ? "Searching…" : "Search"}
        </button>
      </form>
      <LibraryError error={search.error} />
      {search.data ? (
        <p aria-live="polite" className="search-summary">
          {search.data.total_photos} matching{" "}
          {search.data.total_photos === 1 ? "photo" : "photos"}.{" "}
          {search.data.total_events} matching{" "}
          {search.data.total_events === 1 ? "Event" : "Events"}.
          {search.data.has_more
            ? " Refine the search to see fewer results."
            : ""}
        </p>
      ) : null}
      {search.data?.people.length ? (
        <section aria-labelledby="search-people-title">
          <h2 id="search-people-title">People</h2>
          <ul className="search-people">
            {search.data.people.map((person) => (
              <li key={`${person.person_id}-${person.event_id}`}>
                <strong>{person.person_name}</strong> attended part of{" "}
                {person.event_title}.
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {search.data?.events.length ? (
        <section aria-labelledby="search-events-title">
          <h2 id="search-events-title">Events</h2>
          <EventGallery
            events={search.data.events}
            matching
            onOpen={onOpenEvent}
          />
        </section>
      ) : null}
      {search.data?.photos.length ? (
        <section aria-labelledby="search-photos-title">
          <h2 id="search-photos-title">Photos</h2>
          <MediaGallery
            media={search.data.photos}
            onOpen={(item) =>
              onOpenMedia(item, () => model.refreshListingAccess(item.id))
            }
          />
        </section>
      ) : null}
      {search.data &&
      search.data.total_events === 0 &&
      search.data.total_photos === 0 ? (
        <p className="library-empty">
          Nothing in your shared collection matched.
        </p>
      ) : null}
    </div>
  );
}
