import type { Destination } from "./types";

const libraryDestinations: ReadonlyArray<{
  destination: Destination;
  label: string;
}> = [
  { destination: "photos", label: "Photos" },
  { destination: "events", label: "Events" },
  { destination: "favorites", label: "Favorites" },
  { destination: "search", label: "Search" },
];

export function LibraryNavigation({
  className,
  current,
  showBrand = false,
  showSearch = true,
  onNavigate,
}: {
  className: string;
  current?: Destination;
  showBrand?: boolean;
  showSearch?: boolean;
  onNavigate: (destination: Destination) => void;
}) {
  const destinations = showSearch
    ? libraryDestinations
    : libraryDestinations.filter((item) => item.destination !== "search");
  return (
    <nav aria-label="Library navigation" className={className}>
      {showBrand ? <div className="library-brand">Memento</div> : null}
      {destinations.map((item) => (
        <button
          aria-current={current === item.destination ? "page" : undefined}
          key={item.destination}
          onClick={() => onNavigate(item.destination)}
          type="button"
        >
          {item.label}
        </button>
      ))}
    </nav>
  );
}
