import { apiJSON, apiNoContent } from "./api";
import type {
  RequestContract,
  ResponseContract,
} from "./types/generated/contracts";

function directGeneratedResponse(path: string) {
  return apiJSON<ResponseContract>(path);
}

function directGeneratedRequest(request: RequestContract) {
  return apiNoContent("/api/wrapped-request", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export async function allowedDirectHelpers() {
  const request: RequestContract = { name: "generated" };
  await directGeneratedResponse("/api/generated-response");
  await directGeneratedRequest(request);
}
