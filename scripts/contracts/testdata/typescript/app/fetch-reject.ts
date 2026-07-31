const directPath = "/public/status";

function fetchPath(path: string) {
  return fetch(path);
}

const fetchAlias = fetch;
const { fetch: destructuredFetch } = window;
const boundFetch = fetch.bind(undefined);

export async function rejectedFetch(path: string) {
  await fetch("/api/direct");
  await window.fetch(directPath);
  await window["fetch"]("/constant");
  await fetch(path);
  await fetchAlias("/alias");
  await destructuredFetch("/destructured");
  await boundFetch("/bound");
  await fetch.call(undefined, "/call");
  await fetch.apply(undefined, ["/apply"]);
  await fetchPath(path);
}
