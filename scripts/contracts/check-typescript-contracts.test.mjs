import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { checkTypeScriptContracts } from "./check-typescript-contracts.mjs";

const fixtureRoot = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "testdata",
  "typescript",
);

test("reports stable response, request, and direct fetch diagnostics", () => {
  assert.deepEqual(checkTypeScriptContracts(fixtureRoot), [
    "app/reject.ts:10:9 [response-contract]: apiJSON must declare one response type from app/types/generated",
    "app/reject.ts:16:26 [request-contract]: shared API request payload must have generated-type provenance",
    "app/reject.ts:21:9 [direct-api-fetch]: direct /api fetch is only allowed in app/api.ts",
    "app/reject.ts:22:9 [direct-api-fetch]: direct /api fetch is only allowed in app/api.ts",
    "app/reject.ts:28:26 [request-contract]: shared API request payload must have generated-type provenance",
    "app/reject.ts:9:17 [response-contract]: apiJSON response type LocalResponse must be declared in app/types/generated",
  ]);
});

test("accepts generated response and request provenance while leaving UI types alone", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, { include: ["app/allow.ts"] }),
    [],
  );
});
