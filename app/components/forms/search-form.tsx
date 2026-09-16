import { Search, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

// The URL is the search. The field shows an unsent edit while the person
// types and the URL's value otherwise, so submitting, clearing, and browser
// history all land on the URL without a sync step that could miss a change:
// a Clear followed at once by Back can render as one update whose value never
// changed, and a stored draft of "" would then stick.
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
  const [draft, setDraft] = useState<string | null>(null);
  const shown = draft ?? value;
  useEffect(() => {
    // A history change replaces an unsent edit, like a fresh page would.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(null);
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
        setDraft(null);
        onSearch(shown);
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
            value={shown}
          />
          {shown && (
            <Button
              aria-label="Clear search"
              className="absolute inset-y-0 right-0 w-11 p-0 text-muted"
              onClick={() => {
                setDraft(null);
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
