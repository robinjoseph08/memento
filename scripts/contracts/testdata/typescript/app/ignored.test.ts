import { apiJSON } from "./api";

interface TestResponse {
  test: boolean;
}

void apiJSON<TestResponse>("/api/test-only");
void fetch("/api/test-only");
