type LocalRequest = { name: string };
type LocalResponse = { ok: boolean };

export async function apiResponse(
  path: string,
  init?: RequestInit,
): Promise<Response> {
  return fetch(path, init);
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
  const reboundFetch = fetch.bind(undefined);
  void reboundFetch;
}

export async function directFetchWrapper(path: string): Promise<Response> {
  return fetch(path);
}

export async function protectedCallWrapper(
  path: string,
): Promise<LocalResponse> {
  return apiJSON<LocalResponse>(path);
}

export async function protectedAliasWrapper(
  path: string,
): Promise<LocalResponse> {
  const client = apiJSON;
  return client<LocalResponse>(path);
}

export async function handwrittenWrapper(
  path: string,
  request: LocalRequest,
): Promise<LocalResponse> {
  return apiJSON<LocalResponse>(path, {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export function serializedRequestWrapper(request: LocalRequest): RequestInit {
  return { body: JSON.stringify(request) };
}
