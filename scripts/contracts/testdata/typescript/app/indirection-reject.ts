import { apiJSON, apiNoContent } from "./api";

interface LocalRequest {
  name: string;
}

interface LocalResponse {
  id: string;
}

const jsonAlias = apiJSON;
const noContentAlias = apiNoContent;

function localResponseWrapper(path: string) {
  return apiJSON<LocalResponse>(path);
}

function forwardedClientWrapper(client: typeof apiJSON) {
  return client<LocalResponse>("/api/forwarded-client");
}

function localRequestWrapper(request: LocalRequest) {
  return apiNoContent("/api/wrapped-request", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

function forwardOptions(options: RequestInit) {
  return noContentAlias("/api/forwarded-options", options);
}

function localOptions(request: LocalRequest): RequestInit {
  return {
    method: "POST",
    body: JSON.stringify(request),
  };
}

function fetchPath(path: string) {
  return fetch(path);
}

export function rejectedDynamicOptions(options: RequestInit) {
  return apiNoContent("/api/dynamic-options", options);
}

export function rejectedDynamicFetch(path: string) {
  return fetch(path);
}

export async function rejectedIndirection() {
  const request: LocalRequest = { name: "local" };
  await jsonAlias<LocalResponse>("/api/aliased-response");
  await localResponseWrapper("/api/wrapped-response");
  await forwardedClientWrapper(jsonAlias);
  await localRequestWrapper(request);
  await forwardOptions(localOptions(request));

  const apiRoot = "/api";
  const constantPath = apiRoot + "/constant";
  await fetch(constantPath);
  await fetchPath(`${apiRoot}/wrapped`);
}
