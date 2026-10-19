import { isApiError } from "../../../shared/api/api-error";
import { Callout } from "../../../shared/ui/callout";

/**
 * The shift opens by itself with the first sale of the day; this callout appears only when the
 * server refused a sale with `shift_unavailable`.
 */
export function ShiftFallbackCallout({ error }: { error: unknown }) {
  if (!isApiError(error) || error.code !== "shift_unavailable") return null;
  return (
    <Callout tone="warn" role="alert" style={{ marginBottom: 16 }}>
      Chưa có ca bán hàng đang mở.
    </Callout>
  );
}
