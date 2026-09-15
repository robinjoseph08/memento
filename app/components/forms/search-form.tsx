import { Search, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

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
  useEffect(() => {
    // URL history changes arrive after render and must replace any stale draft.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(value);
  }, [value]);
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
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-muted"
            strokeWidth={1.5}
          />
          <Input
            className="pr-11 pl-10 [&::-webkit-search-cancel-button]:appearance-none"
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
              <X aria-hidden="true" className="size-4" strokeWidth={1.5} />
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
