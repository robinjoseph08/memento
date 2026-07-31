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
}
