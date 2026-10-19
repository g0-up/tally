import { errorMessage } from "../api/api-error";
import { Button, ButtonRow } from "./button";
import { Callout } from "./callout";

export const LIST_LOADING_TEXT = "Đang tải…";
export const LOAD_MORE_LABEL = "Xem thêm";

export interface PagedListState {
  isLoading: boolean;
  error: unknown;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => unknown;
  refetch: () => unknown;
}

/** Shown instead of a table while its first page loads. */
export function ListLoading() {
  return (
    <p className="hint" role="status" aria-live="polite">
      {LIST_LOADING_TEXT}
    </p>
  );
}

/** Below a cursor-paged table: the load error with a retry, or "Xem thêm" while pages remain. */
export function ListFooter({ list }: { list: PagedListState }) {
  if (list.error) {
    return (
      <Callout tone="bad" role="alert">
        Không tải được danh sách: {errorMessage(list.error)}{" "}
        <Button size="sm" onClick={() => void list.refetch()}>
          Thử lại
        </Button>
      </Callout>
    );
  }
  if (!list.hasMore) return null;
  return (
    <ButtonRow style={{ marginTop: 12 }}>
      <Button size="sm" onClick={() => void list.loadMore()} pending={list.loadingMore} pendingLabel={LIST_LOADING_TEXT}>
        {LOAD_MORE_LABEL}
      </Button>
    </ButtonRow>
  );
}
