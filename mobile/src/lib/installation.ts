import type { Status } from "@/types/generated/identity";

import type { HTTP } from "./http";
import { supportsServer, UPDATE_NEEDED } from "./version";

// InstallationStatus is what the status endpoint said about the Memento
// server at an origin.
export interface InstallationStatus {
  origin: string;
  version: string;
}

export const NOT_FOUND = "We couldn't find Memento at that address.";

// findInstallation checks an address against the public status endpoint before
// anything else talks to it. It rejects with a message written for the Person:
// NOT_FOUND when the address is not a Memento, UPDATE_NEEDED when it is one
// this app is too new for.
export async function findInstallation(
  http: HTTP,
  signal?: AbortSignal,
): Promise<InstallationStatus> {
  let status: Partial<Status> | undefined;
  try {
    status = await http.request<Partial<Status>>("/api/identity/status", {
      signal,
    });
  } catch {
    throw new Error(NOT_FOUND);
  }
  if (
    typeof status !== "object" ||
    status === null ||
    typeof status.claimed !== "boolean"
  ) {
    throw new Error(NOT_FOUND);
  }
  if (!supportsServer(status.version)) {
    throw new Error(UPDATE_NEEDED);
  }
  return { origin: http.origin, version: status.version };
}
