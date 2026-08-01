import * as sharedAPI from "./api";
import type { ResponseContract } from "./types/generated/contracts";

const apiName = "apiJSON" as const;
declare const unresolvedAPIName: keyof typeof sharedAPI;
const fetchName = "fetch" as const;
declare const unresolvedGlobalName: keyof Window;

export async function rejectedComputedAccess() {
  await sharedAPI["apiJSON"]<ResponseContract>("/api/literal");
  await sharedAPI[apiName]<ResponseContract>("/api/constant");
  const unknownAPI = sharedAPI[unresolvedAPIName];
  void unknownAPI;
  await window["fetch"]("/window-literal");
  await globalThis["fetch"]("/global-literal");
  const constantFetch = window[fetchName];
  await constantFetch("/fetch-alias");
  const unknownGlobal = window[unresolvedGlobalName];
  void unknownGlobal;
  await sharedAPI["apiJSON"].call(undefined, "/api/call");
  const boundFetch = globalThis["fetch"].bind(undefined);
  await boundFetch("/bound");
}
