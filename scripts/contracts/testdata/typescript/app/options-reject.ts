import { apiNoContent } from "./api";
import type { RequestContract } from "./types/generated/contracts";

export function mutableOptions(request: RequestContract) {
  const options: RequestInit = {
    method: "POST",
    body: JSON.stringify(request),
  };
  options.body = JSON.stringify(request);
  return apiNoContent("/api/mutable", options);
}

export function unknownOptions(options: RequestInit) {
  return apiNoContent("/api/unknown", options);
}

function makeOptions(request: RequestContract): RequestInit {
  return {
    method: "POST",
    body: JSON.stringify(request),
  };
}

export function unresolvedOptionsFactory(request: RequestContract) {
  return apiNoContent("/api/factory", makeOptions(request));
}

export function reassignedOptions(request: RequestContract) {
  let options: RequestInit = {
    body: JSON.stringify(request),
  };
  options = { method: "POST", body: JSON.stringify(request) };
  return apiNoContent("/api/reassigned", options);
}
