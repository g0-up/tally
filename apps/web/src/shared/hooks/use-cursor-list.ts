import { useInfiniteQuery, type QueryKey } from "@tanstack/react-query";
import { useMemo } from "react";
import { z, type ZodType } from "zod";
import type { QueryValue } from "../api/http";
import { useApiClient } from "../api/api-context";

// Cursor-paginated list endpoints answer {data: [...], next_cursor: string|null}. This hook keeps
// the pages in the query cache and exposes a flat item list plus "load more".

export interface CursorListOptions<T> {
  queryKey: QueryKey;
  path: string;
  item: ZodType<T>;
  params?: Record<string, QueryValue | readonly string[]>;
  /** Page size, 1..200 (API default 50). */
  limit?: number;
  enabled?: boolean;
}

export function cursorPageSchema<T>(item: ZodType<T>) {
  return z.object({ data: z.array(item), next_cursor: z.string().nullable() });
}

export function useCursorList<T>({ queryKey, path, item, params, limit = 50, enabled = true }: CursorListOptions<T>) {
  if (!Number.isInteger(limit) || limit < 1 || limit > 200) throw new RangeError("limit must be an integer in 1..200");
  const client = useApiClient();
  const schema = useMemo(() => cursorPageSchema(item), [item]);
  const query = useInfiniteQuery({
    queryKey: [...queryKey, { ...params, limit }],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      client.get(path, { query: { ...params, limit, cursor: pageParam }, schema, signal }),
    getNextPageParam: (last) => last.next_cursor,
    enabled,
  });
  const items = useMemo(() => query.data?.pages.flatMap((p) => p.data) ?? [], [query.data]);
  return {
    items,
    hasMore: query.hasNextPage,
    loadMore: () => query.fetchNextPage(),
    loadingMore: query.isFetchingNextPage,
    isLoading: query.isPending && enabled,
    error: query.error,
    refetch: query.refetch,
  };
}
