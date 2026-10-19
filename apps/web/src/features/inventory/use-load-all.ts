import { useEffect } from "react";
import type { PagedListState } from "../../shared/ui/list-footer";

/**
 * Keeps loading the next page of a cursor list while `active`, for a picker or a lookup that must
 * see every row rather than the first page. Stops on a load error; the list's footer offers a retry.
 */
export function useLoadAll(list: PagedListState, active = true) {
  const { hasMore, loadingMore, error, loadMore } = list;
  useEffect(() => {
    if (active && hasMore && !loadingMore && !error) void loadMore();
  }, [active, hasMore, loadingMore, error, loadMore]);
}
