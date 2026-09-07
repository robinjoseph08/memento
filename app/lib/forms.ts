import type { KeyboardEvent } from "react";

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
