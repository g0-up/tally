import type { ReactNode } from "react";
import { ListFooter, ListLoading, type PagedListState } from "../../../shared/ui/list-footer";
import { LoadError } from "./load-error";

/**
 * A cursor-paged console table: "Đang tải…" for the first page, the load error when nothing could
 * be read (never an empty-list text), else the table with "Xem thêm" or a later page's error below.
 */
export function PagedRows({ list, what, children }: { list: PagedListState & { items: readonly unknown[] }; what: string; children: ReactNode }) {
  if (list.isLoading) return <ListLoading />;
  if (list.error && list.items.length === 0) return <LoadError what={what} error={list.error} onRetry={() => void list.refetch()} />;
  return (
    <>
      {children}
      <ListFooter list={list} />
    </>
  );
}
