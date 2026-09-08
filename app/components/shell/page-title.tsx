import { useEffect } from "react";

const productName = "Memento";

export function PageTitle({ title }: { title?: string }) {
  useEffect(() => {
    document.title = title ? `${title} | ${productName}` : productName;
  }, [title]);
  return null;
}
