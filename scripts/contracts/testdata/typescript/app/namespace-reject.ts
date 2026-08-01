import * as sharedAPI from "./api";

const unknownKey: string = "apiJSON";

function erase(value: unknown) {
  return value;
}

const stored = sharedAPI;
erase(sharedAPI);
const erased = sharedAPI as unknown;
const spread = { ...sharedAPI };
void stored;
void erased;
void spread;

export function returnModuleObject() {
  return sharedAPI;
}

export function accessAfterTypeErasure() {
  const asserted = sharedAPI as unknown as Record<string, unknown>;
  return asserted[unknownKey];
}
