export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export async function apiResponse(path: string, init?: RequestInit) {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (response.ok) {
    return response;
  }
  let message = "Memento is unavailable.";
  try {
    const payload = (await response.json()) as {
      error?: { message?: string };
    };
    if (payload.error?.message) {
      message = payload.error.message;
    }
  } catch {
    // The safe fallback does not expose response internals.
  }
  throw new APIError(message, response.status);
}

export async function apiJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await apiResponse(path, init);
  return (await response.json()) as T;
}

export async function apiNoContent(
  path: string,
  init: RequestInit,
): Promise<void> {
  await apiResponse(path, init);
}
