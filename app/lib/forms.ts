import { createContext, type KeyboardEvent, type RefObject } from "react";

// Account actions consult the same unsaved state as route navigation.
export const UnsavedChangesContext = createContext<RefObject<boolean> | null>(
  null,
);

// Call after inline errors render so keyboard users land on the first invalid field.
export function focusFirstInvalid(form: HTMLFormElement | null) {
  form?.querySelector<HTMLElement>('[aria-invalid="true"], :invalid')?.focus();
}

// Attach to a textarea to submit without taking away Enter for new lines.
export function submitOnModEnter(event: KeyboardEvent<HTMLTextAreaElement>) {
  if (
    event.key === "Enter" &&
    (event.metaKey || event.ctrlKey) &&
    !event.nativeEvent.isComposing
  ) {
    event.preventDefault();
    event.currentTarget.form?.requestSubmit();
  }
}
