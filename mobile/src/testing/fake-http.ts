import { HTTPError, type CreateHTTP } from "@/lib/http";

type Reply = unknown | (() => unknown);

// fakeHTTP stands in for the one HTTP adapter. Give it a reply per
// Installation origin and path; a function reply may throw to fail the
// request. Anything unlisted fails the way an unreachable server does. Every
// request is recorded with the session token it carried and, for a POST,
// its body.
export function fakeHTTP(installations: Record<string, Record<string, Reply>>) {
  const requests: { url: string; token: string | null; body?: unknown }[] = [];
  const create: CreateHTTP = (origin, token = null) => ({
    origin,
    request: async <T>(path: string, options?: { body?: unknown }) => {
      requests.push({
        url: origin + path,
        token,
        ...(options?.body === undefined ? {} : { body: options.body }),
      });
      const replies = installations[origin];
      if (!replies || !(path in replies)) {
        throw new TypeError("Network request failed");
      }
      const reply = replies[path];
      return (typeof reply === "function" ? reply() : reply) as T;
    },
    media: (path) =>
      token
        ? { uri: origin + path, headers: { Authorization: `Bearer ${token}` } }
        : { uri: origin + path },
  });
  return { create, requests };
}

export function notFound() {
  throw new HTTPError("Not found.", 404);
}

export function unauthenticated() {
  throw new HTTPError("Sign in to continue.", 401);
}
