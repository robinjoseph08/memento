import type { Destination } from "./types";

export function destinationFromPath(pathname: string): Destination {
  if (pathname === "/events" || pathname.startsWith("/events/")) {
    return "events";
  }
  if (pathname === "/favorites") return "favorites";
  if (pathname === "/search") return "search";
  return "photos";
}

export function destinationPath(destination: Destination) {
  return destination === "photos" ? "/photos" : `/${destination}`;
}

export function eventIDFromPath(pathname: string) {
  const encodedID = pathname.match(/^\/events\/([^/]+)$/)?.[1];
  return encodedID ? decodeURIComponent(encodedID) : undefined;
}

export function captureDateFromSearch(search: string) {
  const value = new URLSearchParams(search).get("date");
  if (value === "undated") return null;
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return undefined;
  const parsed = new Date(`${value}T00:00:00Z`);
  return Number.isNaN(parsed.valueOf()) ||
    parsed.toISOString().slice(0, 10) !== value
    ? undefined
    : value;
}

export function captureDateSearch(captureDate: string | null | undefined) {
  if (captureDate === undefined) return "";
  const params = new URLSearchParams({
    date: captureDate ?? "undated",
  });
  return `?${params.toString()}`;
}
