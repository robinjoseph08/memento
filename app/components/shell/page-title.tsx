import { useLayoutEffect } from "react";

const productName = "Memento";

export function PageTitle({ title }: { title?: string }) {
  useLayoutEffect(() => {
    document.title = title ? `${title} | ${productName}` : productName;
  }, [title]);
  return null;
}
