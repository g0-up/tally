import { errorMessage, isApiError } from "../../../shared/api/api-error";
import { Callout } from "../../../shared/ui/callout";
import { DrawerError } from "../../../shared/ui/detail-drawer";
import { ListLoading } from "../../../shared/ui/list-footer";

export interface DrawerStatusProps {
  error: unknown;
  /** Callout when the document no longer exists ("Báo giá đã bị xóa."). */
  deletedText: string;
}

/** Body of a sales drawer while its document loads or when it failed to load. */
export function DrawerStatus({ error, deletedText }: DrawerStatusProps) {
  if (error === null || error === undefined) return <ListLoading />;
  if (isApiError(error) && error.status === 404) return <Callout tone="info">{deletedText}</Callout>;
  return <DrawerError message={`Không tải được chứng từ: ${errorMessage(error)}`} />;
}
