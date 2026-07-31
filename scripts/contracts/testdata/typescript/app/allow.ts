import { apiJSON, apiNoContent } from "./api";
import type {
  RequestContract,
  ResponseContract,
} from "./types/generated/contracts";

type UIState = Record<string, boolean>;
const expanded: UIState = { details: true };
void expanded;

function generatedRequest(): RequestContract {
  return { name: "helper" };
}

export async function allowed() {
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
  await fetch("/public/status");
}
