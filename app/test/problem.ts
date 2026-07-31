import type { ProblemResponse } from "../types/generated/errcodes";

export function problemResponse(
  message: string,
  status: number,
  code = "test_error",
): ProblemResponse {
  return {
    error: {
      code,
      message,
      status_code: status,
    },
  };
}
