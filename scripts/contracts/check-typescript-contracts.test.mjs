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
      "app/reject.ts:21:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/reject.ts:22:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/reject.ts:28:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/reject.ts:9:17 [response-contract]: apiJSON response type LocalResponse must be declared in app/types/generated",
    ],
  );
});

test("rejects shared API aliases, destructuring, bind/call/apply, parameters, returns, and unresolved generic helpers", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/indirection-reject.ts"],
    }),
    [
      "app/indirection-reject.ts:10:19 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:11:9 [shared-api-indirection]: apiNoContent must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:12:23 [shared-api-indirection]: apiResponse must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:14:38 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:19:10 [shared-api-indirection]: apiResponse must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:23:18 [response-contract]: apiJSON response type T must be declared in app/types/generated",
      "app/indirection-reject.ts:27:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:31:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:32:9 [shared-api-indirection]: apiNoContent must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:33:22 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
    ],
  );
});

test("rejects mutable, unknown, reassigned, and factory-produced request options", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/options-reject.ts"],
    }),
    [
      "app/options-reject.ts:10:39 [request-contract]: shared API request options must be an immutable object-literal, conditional, or spread graph",
      "app/options-reject.ts:14:39 [request-contract]: shared API request options must be an immutable object-literal, conditional, or spread graph",
      "app/options-reject.ts:25:39 [request-contract]: shared API request options must be an immutable object-literal, conditional, or spread graph",
      "app/options-reject.ts:33:42 [request-contract]: shared API request options must be an immutable object-literal, conditional, or spread graph",
    ],
  );
});

test("rejects every global fetch form outside app/api.ts", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/fetch-reject.ts"],
    }),
    [
      "app/fetch-reject.ts:12:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:13:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:14:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:15:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:19:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:20:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:4:10 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:7:20 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:8:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/fetch-reject.ts:9:20 [direct-fetch]: global fetch is only allowed in app/api.ts",
    ],
  );
});

test("accepts direct generated calls, direct generated helpers, immutable options, and non-wire UI state", () => {
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
