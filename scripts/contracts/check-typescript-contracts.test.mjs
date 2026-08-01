import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { checkTypeScriptContracts } from "./check-typescript-contracts.mjs";

const testdataRoot = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "testdata",
);
const fixtureRoot = path.join(testdataRoot, "typescript");
const unsafeAPIFixtureRoot = path.join(testdataRoot, "typescript-api-reject");

test("reports stable response, request, and direct fetch diagnostics", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, { include: ["app/reject.ts"] }),
    [
      "app/reject.ts:10:9 [response-contract]: apiJSON must declare one response type from a configured Tygo output",
      "app/reject.ts:16:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/reject.ts:21:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/reject.ts:22:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/reject.ts:28:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/reject.ts:9:17 [response-contract]: apiJSON response type LocalResponse must be declared in a configured Tygo output",
    ],
  );
});

test("accepts generated provenance only from exact tygo output files", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/provenance-reject.ts"],
    }),
    [
      "app/provenance-reject.ts:11:26 [request-contract]: shared API request payload must have generated-type provenance",
      "app/provenance-reject.ts:8:17 [response-contract]: apiJSON response type HandwrittenResponse must be declared in a configured Tygo output",
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
      "app/indirection-reject.ts:1:8 [shared-api-namespace]: namespace imports from app/api.ts are forbidden; import its functions by name",
      "app/indirection-reject.ts:23:18 [response-contract]: apiJSON response type T must be declared in a configured Tygo output",
      "app/indirection-reject.ts:27:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:31:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:32:9 [shared-api-indirection]: apiNoContent must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
      "app/indirection-reject.ts:33:22 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
    ],
  );
});

test("rejects computed protected API and global fetch access", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/computed-reject.ts"],
    }),
    [
      "app/computed-reject.ts:10:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped; element access is forbidden",
      "app/computed-reject.ts:11:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped; element access is forbidden",
      "app/computed-reject.ts:12:22 [shared-api-indirection]: computed access to shared API exports is forbidden because the key cannot be resolved safely",
      "app/computed-reject.ts:14:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/computed-reject.ts:15:9 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/computed-reject.ts:16:25 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/computed-reject.ts:18:25 [direct-fetch]: global fetch is only allowed in app/api.ts",
      "app/computed-reject.ts:1:8 [shared-api-namespace]: namespace imports from app/api.ts are forbidden; import its functions by name",
      "app/computed-reject.ts:20:9 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped; element access is forbidden",
      "app/computed-reject.ts:21:22 [direct-fetch]: global fetch is only allowed in app/api.ts",
    ],
  );
});

test("rejects app/api.ts namespace imports before unknown and assertion erasure", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/namespace-reject.ts"],
    }),
    [
      "app/namespace-reject.ts:1:8 [shared-api-namespace]: namespace imports from app/api.ts are forbidden; import its functions by name",
    ],
  );
});

test("rejects DOM Response JSON decoding outside app/api.ts", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, {
      include: ["app/response-json-reject.ts"],
    }),
    [
      "app/response-json-reject.ts:12:9 [response-json]: decode JSON HTTP responses with apiJSON instead of Response.json()",
      "app/response-json-reject.ts:13:9 [response-json]: decode JSON HTTP responses with apiJSON instead of Response.json()",
      "app/response-json-reject.ts:15:9 [response-json]: decode JSON HTTP responses with apiJSON instead of Response.json()",
    ],
  );
});

test("rejects exported app/api.ts transport wrappers", () => {
  assert.deepEqual(checkTypeScriptContracts(unsafeAPIFixtureRoot), [
    "app/api.ts:21:24 [api-transport-surface]: only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
    "app/api.ts:25:23 [api-transport-surface]: only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
    "app/api.ts:29:23 [api-transport-surface]: only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
    "app/api.ts:35:23 [api-transport-surface]: only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
    "app/api.ts:38:18 [shared-api-indirection]: apiJSON must be called directly and cannot be stored, passed, returned, rebound, or wrapped",
    "app/api.ts:42:23 [api-transport-surface]: only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
    "app/api.ts:52:17 [api-transport-surface]: only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
  ]);
});

test("accepts the existing app/api.ts transport and validation shape", () => {
  assert.deepEqual(
    checkTypeScriptContracts(fixtureRoot, { include: ["app/api.ts"] }),
    [],
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
