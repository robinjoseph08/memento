import { apiJSON, apiNoContent } from "./api";
import type {
  HandwrittenRequest,
  HandwrittenResponse,
} from "./types/generated/handwritten";

export async function rejectHandwrittenGeneratedDirectoryTypes() {
  await apiJSON<HandwrittenResponse>("/api/handwritten-response");
  await apiNoContent("/api/handwritten-request", {
    method: "POST",
    body: JSON.stringify({ name: "handwritten" } as HandwrittenRequest),
  });
}
