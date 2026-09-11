import { Check, ChevronDown } from "lucide-react";
import { useId, useRef, useState, type ReactNode } from "react";

import { cn } from "../../lib/utils";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "./command";
import { Popover, PopoverContent, PopoverTrigger } from "./popover";

export type ComboboxOption = {
  value: string;
  label: string;
  description?: string;
  leading?: ReactNode;
};

// The one selection control. Every choice between records or enumerations uses
// this instead of a native select. Search is always present so people never
// have to guess whether typing will work.
export function Combobox({
  value,
  onChange,
  options,
  action,
  placeholder = "Choose…",
  searchPlaceholder = "Search…",
  emptyText = "No matches.",
  className,
  disabled,
  ...triggerProps
}: {
  value: string;
  onChange: (value: string) => void;
  options: ComboboxOption[];
  // A choice that stays visible under every search, such as creating a record.
  action?: ComboboxOption;
  placeholder?: string;
  searchPlaceholder?: string;
  emptyText?: string;
  className?: string;
  disabled?: boolean;
  id?: string;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const listID = useId();
  const commandRef = useRef<HTMLDivElement>(null);
  const selected =
    options.find((option) => option.value === value) ??
    (action?.value === value ? action : undefined);
  const item = (option: ComboboxOption) => (
    <CommandItem
      key={option.value}
      keywords={option.description ? [option.description] : undefined}
      onSelect={() => {
        onChange(option.value);
        setOpen(false);
      }}
      value={option.value}
    >
      {option.leading && <span aria-hidden="true">{option.leading}</span>}
      <span className="min-w-0 flex-1">
        <span className="block truncate">{option.label}</span>
        {option.description && (
          <span className="block truncate text-xs text-muted">
            {option.description}
          </span>
        )}
      </span>
      <Check
        aria-hidden="true"
        className={cn(
          "size-4 shrink-0",
          option.value === value ? "opacity-100" : "opacity-0",
        )}
        strokeWidth={2}
      />
    </CommandItem>
  );
  return (
    <Popover onOpenChange={setOpen} open={open}>
      <PopoverTrigger
        aria-controls={open ? listID : undefined}
        aria-expanded={open}
        className={cn(
          "flex min-h-11 w-full cursor-pointer items-center justify-between gap-2 rounded-sm border border-border bg-background px-3 py-2 text-left text-sm focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive",
          className,
        )}
        disabled={disabled}
        role="combobox"
        type="button"
        {...triggerProps}
      >
        <span className={cn("min-w-0 truncate", !selected && "text-muted")}>
          {selected?.label ?? placeholder}
        </span>
        <ChevronDown
          aria-hidden="true"
          className="size-4 shrink-0 text-muted"
          strokeWidth={1.5}
        />
      </PopoverTrigger>
      <PopoverContent
        className="w-(--radix-popover-trigger-width) min-w-56 p-0"
        // Keyboard handling lives on the command root, so focus lands there
        // (or on its search box) rather than on the popover wrapper.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          const root = commandRef.current;
          (root?.querySelector("input") ?? root)?.focus();
        }}
      >
        <Command
          className="outline-hidden"
          ref={commandRef}
          tabIndex={-1}
          // cmdk matches on the item value by default; labels and descriptions
          // are the searchable text here, values are opaque IDs.
          filter={(itemValue, search, keywords) => {
            const option =
              options.find((candidate) => candidate.value === itemValue) ??
              (action?.value === itemValue ? action : undefined);
            const haystack = [option?.label ?? itemValue, ...(keywords ?? [])]
              .join(" ")
              .toLowerCase();
            return haystack.includes(search.trim().toLowerCase()) ? 1 : 0;
          }}
        >
          <CommandInput placeholder={searchPlaceholder} />
          <CommandList id={listID}>
            <CommandEmpty>{emptyText}</CommandEmpty>
            <CommandGroup>{options.map(item)}</CommandGroup>
            {action && (
              <>
                <CommandSeparator />
                <CommandGroup forceMount>
                  <CommandItem
                    forceMount
                    onSelect={() => {
                      onChange(action.value);
                      setOpen(false);
                    }}
                    value={action.value}
                  >
                    {action.leading && (
                      <span aria-hidden="true">{action.leading}</span>
                    )}
                    <span className="min-w-0 flex-1 truncate">
                      {action.label}
                    </span>
                  </CommandItem>
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
