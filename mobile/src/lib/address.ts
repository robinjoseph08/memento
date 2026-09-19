export type ParsedAddress = { origin: string } | { error: string };

const NOT_AN_ADDRESS =
  "That doesn't look like a web address. Check it and try again.";

// parseAddress turns what a Person typed or pasted into an Installation
// origin. Anything after the host is dropped, so a pasted Album link works.
// Plain http is for development; pass allowHTTP only from a development build.
export function parseAddress(
  input: string,
  { allowHTTP }: { allowHTTP: boolean },
): ParsedAddress {
  const trimmed = input.trim();
  if (!trimmed) {
    return { error: "Enter your Memento address." };
  }
  if (/\s/.test(trimmed)) {
    return { error: NOT_AN_ADDRESS };
  }
  let url: URL;
  try {
    url = new URL(
      /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`,
    );
  } catch {
    return { error: NOT_AN_ADDRESS };
  }
  if (url.protocol === "http:" && !allowHTTP) {
    return {
      error:
        "Memento addresses start with https://. Check the address and try again.",
    };
  }
  if (
    (url.protocol !== "https:" && url.protocol !== "http:") ||
    !url.hostname
  ) {
    return { error: NOT_AN_ADDRESS };
  }
  return { origin: `${url.protocol}//${url.host}` };
}
