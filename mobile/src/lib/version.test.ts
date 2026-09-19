import { MIN_SERVER_VERSION, supportsServer } from "./version";

describe("supportsServer", () => {
  it("accepts the oldest supported release and anything newer", () => {
    expect(supportsServer(MIN_SERVER_VERSION)).toBe(true);
    expect(supportsServer(`v${MIN_SERVER_VERSION}`)).toBe(true);
    expect(supportsServer("v1.0.0")).toBe(true);
    expect(supportsServer("0.10.0")).toBe(true);
    expect(supportsServer("v2.0.0-rc.1")).toBe(true);
  });

  it("rejects older releases", () => {
    expect(supportsServer("v0.2.0")).toBe(false);
    expect(supportsServer("0.1.9")).toBe(false);
  });

  it("rejects an Installation too old to report a version", () => {
    expect(supportsServer(undefined)).toBe(false);
    expect(supportsServer("")).toBe(false);
  });

  it("accepts development builds, which are not releases", () => {
    expect(supportsServer("dev")).toBe(true);
    expect(supportsServer("6d5905f")).toBe(true);
    expect(supportsServer("v0.2.0-4-g6d5905f")).toBe(true);
    expect(supportsServer("v0.2.0-4-g6d5905f-dirty")).toBe(true);
    expect(supportsServer("v0.2.0-dirty")).toBe(true);
  });
});
