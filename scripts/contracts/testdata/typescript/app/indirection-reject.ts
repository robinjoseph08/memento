import * as sharedAPI from "./api";
import {
  apiJSON,
  apiJSON as importedJSONAlias,
  apiNoContent,
  apiResponse,
} from "./api";
import type { ResponseContract } from "./types/generated/contracts";

const jsonAlias = apiJSON;
const { apiNoContent: destructuredNoContent } = sharedAPI;
const boundResponse = apiResponse.bind(undefined, "/api/bound");

function acceptClient(client: typeof apiJSON) {
  return client<ResponseContract>("/api/parameter");
}

function returnClient() {
  return apiResponse;
}

function unresolvedGenericHelper<T>(path: string) {
  return apiJSON<T>(path);
}

export async function rejectedSharedAPIIndirection() {
  await importedJSONAlias<ResponseContract>("/api/import-alias");
  await jsonAlias<ResponseContract>("/api/alias");
  await destructuredNoContent("/api/destructured", {});
  await boundResponse();
  await apiJSON.call(undefined, "/api/call");
  await apiNoContent.apply(undefined, ["/api/apply", {}]);
  await acceptClient(apiJSON);
  await unresolvedGenericHelper<ResponseContract>("/api/generic-helper");
  return returnClient();
}
