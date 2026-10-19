import { useEffect, useState } from "react";

/**
 * Moves focus to the first marked (`aria-invalid="true"`) input of the dialog the user is in, or of
 * the page outside a dialog, once the marks are drawn. Call it right after setting the errors.
 */
export function useFocusFirstInvalid(): () => void {
  const [request, setRequest] = useState<{ root: ParentNode } | null>(null);
  useEffect(() => {
    request?.root.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }, [request]);
  return () => setRequest({ root: document.activeElement?.closest('[role="dialog"]') ?? document });
}
