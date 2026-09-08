import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function formatDate(value: string) {
  return new Date(value).toLocaleString();
}

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
