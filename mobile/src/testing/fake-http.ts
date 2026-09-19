import { HTTPError, type CreateHTTP } from "@/lib/http";

type Reply = unknown | (() => unknown);

// fakeHTTP stands in for the one HTTP adapter. Give it a reply per
// Installation origin and path; a function reply may throw to fail the
// request. Anything unlisted fails the way an unreachable server does.
export function fakeHTTP(installations: Record<string, Record<string, Reply>>) {
  const requests: string[] = [];
  const create: CreateHTTP = (origin) => ({
    origin,
    request: async <T>(path: string) => {
      requests.push(origin + path);
      const replies = installations[origin];
      if (!replies || !(path in replies)) {
        throw new TypeError("Network request failed");
      }
      const reply = replies[path];
      return (typeof reply === "function" ? reply() : reply) as T;
    },
  });
  return { create, requests };
}

export function notFound() {
  throw new HTTPError("Not found.", 404);
}
