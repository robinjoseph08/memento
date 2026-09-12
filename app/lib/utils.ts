import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function formatDate(value: string) {
  return new Date(value).toLocaleString();
}

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Copy search params with some values set, or removed when null.
export function withParams(
  current: URLSearchParams,
  values: Record<string, string | null>,
) {
  const next = new URLSearchParams(current);
  for (const [key, value] of Object.entries(values)) {
    if (value === null) next.delete(key);
    else next.set(key, value);
  }
  return next;
}
