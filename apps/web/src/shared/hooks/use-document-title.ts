import { useEffect } from "react";

/** Product name (as in index.html) shown after the page name in the browser tab and history. */
export const TITLE_SUFFIX = "Tally";

export function documentTitle(page: string | null | undefined): string {
  const name = page?.trim();
  return name ? `${name} · ${TITLE_SUFFIX}` : TITLE_SUFFIX;
}

/** Names the browser tab after the current page so screen-reader and tab users know where they are. */
export function useDocumentTitle(page: string | null | undefined): void {
  const title = documentTitle(page);
  useEffect(() => {
    document.title = title;
  }, [title]);
}
