import { act, renderHook } from "@testing-library/react";
import { expect, test } from "vitest";

import { useRebasedDraft } from "./useRebasedDraft";

test("rebases untouched draft fields while preserving local edits", () => {
  const { result, rerender } = renderHook(
    ({ server }) => useRebasedDraft(server),
    {
      initialProps: {
        server: { preference: "immediate", timezone: "America/New_York" },
      },
    },
  );

  act(() => {
    result.current.setDraft({
      preference: "weekly",
      timezone: "America/New_York",
    });
  });
  rerender({
    server: { preference: "none", timezone: "America/Los_Angeles" },
  });

  expect(result.current.draft).toEqual({
    preference: "weekly",
    timezone: "America/Los_Angeles",
  });
  expect(result.current.hasStaleConflict).toBe(true);

  act(() => {
    result.current.setDraft({
      preference: "weekly",
      timezone: "Europe/London",
    });
  });
  expect(result.current.draft).toEqual({
    preference: "weekly",
    timezone: "Europe/London",
  });
  expect(result.current.hasStaleConflict).toBe(true);

  act(() => result.current.resetToServer());
  expect(result.current.draft).toEqual({
    preference: "none",
    timezone: "America/Los_Angeles",
  });
  expect(result.current.hasStaleConflict).toBe(false);
});

test("does not report a conflict when only untouched fields change", () => {
  const { result, rerender } = renderHook(
    ({ server }) => useRebasedDraft(server),
    {
      initialProps: {
        server: { preference: "immediate", timezone: "America/New_York" },
      },
    },
  );

  act(() => {
    result.current.setDraft({
      preference: "weekly",
      timezone: "America/New_York",
    });
  });
  rerender({
    server: { preference: "immediate", timezone: "America/Los_Angeles" },
  });

  expect(result.current.draft).toEqual({
    preference: "weekly",
    timezone: "America/Los_Angeles",
  });
  expect(result.current.hasStaleConflict).toBe(false);
});
