import { apiJSON, apiNoContent } from "./api";
import type { ResponseContract } from "./types/generated/contracts";

interface LocalResponse {
  id: string;
}

export async function localResponse() {
  await apiJSON<LocalResponse>("/api/local");
  await apiJSON("/api/missing-type");
}

export async function inferredRequest() {
  const request = { name: "inferred" };
  await apiNoContent("/api/request", {
    body: JSON.stringify(request),
  });
}

export async function directFetch() {
  await fetch("/api/private");
  await window.fetch("/api/window-private");
}

export async function mapRequest() {
  const request: Record<string, string> = { name: "map" };
  await apiJSON<ResponseContract>("/api/map", {
    body: JSON.stringify(request),
  });
}
