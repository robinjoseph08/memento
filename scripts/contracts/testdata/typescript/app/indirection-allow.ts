import { apiJSON, apiNoContent } from "./api";
import type {
  RequestContract,
  ResponseContract,
} from "./types/generated/contracts";

interface LocalDialogState {
  id: string;
}

const dialogByID: Record<string, LocalDialogState> = {
  dialog: { id: "dialog" },
};
void dialogByID;

const jsonAlias = apiJSON;
const noContentAlias = apiNoContent;

function generatedResponseWrapper(path: string) {
  return jsonAlias<ResponseContract>(path);
}

function forwardedClientWrapper(client: typeof apiJSON) {
  return client<ResponseContract>("/api/forwarded-client");
}

function genericResponseWrapper<T>(path: string) {
  return jsonAlias<T>(path);
}

function nestedGenericResponseWrapper<T>(path: string) {
  return genericResponseWrapper<T>(path);
}

function generatedRequestWrapper(request: RequestContract) {
  return noContentAlias("/api/wrapped-request", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

function generatedOptions(request: RequestContract): RequestInit {
  return {
    method: "POST",
    body: JSON.stringify(request),
  };
}

function forwardOptions(options: RequestInit) {
  return apiNoContent("/api/forwarded-options", options);
}

function fetchPath(path: string) {
  return fetch(path);
}

export async function allowedDynamicAsset(assetID: string) {
  await fetch(`/public/assets/${assetID}`);
}

export async function allowedIndirection() {
  const request: RequestContract = { name: "generated" };
  await generatedResponseWrapper("/api/wrapped-response");
  await forwardedClientWrapper(jsonAlias);
  await nestedGenericResponseWrapper<ResponseContract>("/api/generic-response");
  await generatedRequestWrapper(request);
  await forwardOptions(generatedOptions(request));

  const publicRoot = "/public";
  const statusPath = publicRoot + "/status";
  await fetch(statusPath);
  await fetchPath(`${publicRoot}/wrapped`);
}
