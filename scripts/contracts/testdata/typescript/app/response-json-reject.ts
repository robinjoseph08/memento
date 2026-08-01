import { apiJSON, apiResponse } from "./api";
import type { ResponseContract } from "./types/generated/contracts";

class LocalResponse {
  async json(): Promise<unknown> {
    return {};
  }
}

export async function rejectHandwrittenResponseJSON() {
  const response = await apiResponse("/api/response");
  await response.json();
  await (await apiResponse("/api/chained-response")).json();
  const asserted = response as Response;
  await asserted.json();

  await response.blob();
  await apiJSON<ResponseContract>("/api/allowed-json");
  await new LocalResponse().json();
}
