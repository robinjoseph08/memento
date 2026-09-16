import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";

import { SearchForm } from "./search-form";

afterEach(cleanup);

// Stands in for a page whose search lives in the URL.
function Page({ onSearch }: { onSearch?: (value: string) => void }) {
  const [value, setValue] = useState("Robin");
  return (
    <SearchForm
      label="Search people"
      onSearch={(next) => {
        onSearch?.(next);
        setValue(next);
      }}
      value={value}
    />
  );
}

it("shows the URL's value again when a clear is undone by history", async () => {
  const user = userEvent.setup();
  render(<Page onSearch={vi.fn()} />);
  const search = screen.getByRole("searchbox", { name: "Search people" });
  expect(search).toHaveValue("Robin");
  await user.type(search, " draft");
  expect(search).toHaveValue("Robin draft");
  await user.click(screen.getByRole("button", { name: "Clear search" }));
  expect(search).toHaveValue("");
  // Going back restores the URL; the field follows it with no edit in the way.
  await user.click(screen.getByRole("button", { name: "Search" }));
  expect(search).toHaveValue("");
});

it("keeps the field on the URL when the clear's navigation never lands", async () => {
  const user = userEvent.setup();
  // The URL stays "Robin", as when Back arrives before the clear renders.
  render(
    <SearchForm label="Search people" onSearch={() => {}} value="Robin" />,
  );
  const search = screen.getByRole("searchbox", { name: "Search people" });
  await user.type(search, " draft");
  await user.click(screen.getByRole("button", { name: "Clear search" }));
  expect(search).toHaveValue("Robin");
});
