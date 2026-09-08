import { useId, useRef, useState } from "react";

import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

export function SearchForm({
  label,
  value,
  onSearch,
  className,
}: {
  label: string;
  value: string;
  onSearch: (value: string) => void;
  className?: string;
}) {
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(value);
  const [previousValue, setPreviousValue] = useState(value);
  if (value !== previousValue) {
    setPreviousValue(value);
    setDraft(value);
  }
  return (
    <form
      aria-label={label}
      className={cn(
        "grid max-w-xl grid-cols-[minmax(0,1fr)_auto] items-end gap-2",
        className,
      )}
      onSubmit={(event) => {
        event.preventDefault();
        onSearch(draft);
        inputRef.current?.focus();
      }}
      role="search"
    >
      <div className="min-w-0">
        <label className="mb-2 block text-xs font-medium" htmlFor={id}>
          {label}
        </label>
        <div className="relative">
          <Input
            className="pr-11 [&::-webkit-search-cancel-button]:appearance-none"
            id={id}
            name="q"
            onChange={(event) => setDraft(event.target.value)}
            ref={inputRef}
            type="search"
            value={draft}
          />
          {draft && (
            <Button
              aria-label="Clear search"
              className="absolute inset-y-0 right-0 w-11 p-0 text-muted"
              onClick={() => {
                setDraft("");
                onSearch("");
                inputRef.current?.focus();
              }}
              type="button"
              variant="ghost"
            >
              <svg
                aria-hidden="true"
                className="size-4"
                fill="none"
                stroke="currentColor"
                strokeLinecap="round"
                strokeWidth="1.5"
                viewBox="0 0 24 24"
              >
                <path d="m6 6 12 12M6 18 18 6" />
              </svg>
            </Button>
          )}
        </div>
      </div>
      <Button type="submit" variant="outline">
        Search
      </Button>
    </form>
  );
}
