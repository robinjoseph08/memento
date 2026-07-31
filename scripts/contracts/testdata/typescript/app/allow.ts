import { apiJSON, apiNoContent, apiResponse } from "./api";
import type {
  RequestContract,
  ResponseContract,
} from "./types/generated/contracts";

type UIState = Record<string, boolean>;
const expanded: UIState = { details: true };
const transportLabels = new Map<string, string>([["fetch", "Loading"]]);
void expanded;
void transportLabels;

function generatedRequest(): RequestContract {
  return { name: "helper" };
}

function generatedResponseHelper(path: string) {
  return apiJSON<ResponseContract>(path);
}

function generatedRequestHelper(request: RequestContract) {
  return apiNoContent("/api/helper", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export async function allowed(condition: boolean) {
  const annotated: RequestContract = { name: "annotated" };
  await apiJSON<ResponseContract>("/api/one", {
    method: "POST",
    body: JSON.stringify(annotated),
  });
  await apiNoContent("/api/two", {
    method: "POST",
    body: JSON.stringify({ name: "satisfies" } satisfies RequestContract),
  });
  await apiNoContent("/api/three", {
    method: "POST",
    body: JSON.stringify({ name: "asserted" } as RequestContract),
  });
  await apiNoContent("/api/four", {
    method: "POST",
    body: JSON.stringify(generatedRequest()),
  });

  const body = JSON.stringify(annotated);
  const base = { method: "POST" };
  const options = condition ? { ...base, body } : { ...base };
  await apiNoContent("/api/immutable-options", options);
  await apiResponse("/downloads/archive", { headers: { Accept: "zip" } });
  await generatedResponseHelper("/api/helper-response");
  await generatedRequestHelper(annotated);
}
