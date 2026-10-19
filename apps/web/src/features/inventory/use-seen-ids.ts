import { useCallback, useState } from "react";

/**
 * Ids of the list rows that have come near the viewport, so a page loads a row's details only once
 * the row is shown instead of one request per loaded row up front.
 */
export function useSeenIds() {
  const [seen, setSeen] = useState<ReadonlySet<string>>(() => new Set());
  const markSeen = useCallback((id: string) => setSeen((s) => (s.has(id) ? s : new Set(s).add(id))), []);
  return { seen, markSeen };
}
