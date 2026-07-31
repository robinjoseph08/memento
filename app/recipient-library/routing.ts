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
