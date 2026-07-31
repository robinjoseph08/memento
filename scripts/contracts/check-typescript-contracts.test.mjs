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
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, { include: ["app/reject.ts"] }),
    [
      "app/reject.ts:10:9 [response-contract]: apiJSON must declare one response type from app/types/generated",
      "app/reject.ts:16:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/reject.ts:21:9 [direct-api-fetch]: direct /api fetch is only allowed in app/api.ts",
      "app/reject.ts:22:9 [direct-api-fetch]: direct /api fetch is only allowed in app/api.ts",
      "app/reject.ts:28:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/reject.ts:9:17 [response-contract]: apiJSON response type LocalResponse must be declared in app/types/generated",
    ],
  );
});

test("rejects aliases, wrappers, helper options, request parameters, and constant API fetches", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/indirection-reject.ts"],
    }),
    [
      "app/indirection-reject.ts:15:18 [response-contract]: apiJSON response type LocalResponse must be declared in app/types/generated",
      "app/indirection-reject.ts:19:17 [response-contract]: apiJSON response type LocalResponse must be declared in app/types/generated",
      "app/indirection-reject.ts:25:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/indirection-reject.ts:36:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/indirection-reject.ts:41:10 [direct-api-fetch]: direct /api fetch is only allowed in app/api.ts",
      "app/indirection-reject.ts:45:47 [request-contract]: shared API request options could not be resolved deterministically",
      "app/indirection-reject.ts:49:10 [direct-api-fetch]: fetch URL could not be resolved deterministically; possible /api traffic must use app/api.ts",
      "app/indirection-reject.ts:54:19 [response-contract]: apiJSON response type LocalResponse must be declared in app/types/generated",
      "app/indirection-reject.ts:62:9 [direct-api-fetch]: direct /api fetch is only allowed in app/api.ts",
    ],
  );
});

test("accepts generated aliases and wrappers while leaving UI and non-API types alone", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/allow.ts", "app/indirection-allow.ts"],
    }),
    [],
  );
});

test("keeps test and spec files outside production enforcement", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/ignored.test.ts"],
    }),
    [],
  );
});
