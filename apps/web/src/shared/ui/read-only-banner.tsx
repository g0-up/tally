import { Callout } from "./callout";

export const READ_ONLY_TITLE = "Cửa hàng đang ở chế độ chỉ xem.";
export const READ_ONLY_TEXT = "Gói thuê bao đã hết hạn hoặc đang trong thời gian gia hạn. Bạn vẫn xem và xuất dữ liệu được; ghi sổ mới tạm khóa cho đến khi gia hạn.";

/** Shown above the page when the subscription is read-only or in grace: posting is blocked, reading and export are not. */
export function ReadOnlyBanner() {
  return (
    <Callout tone="warn" className="read-only-banner" role="status">
      <b>{READ_ONLY_TITLE}</b> {READ_ONLY_TEXT}
    </Callout>
  );
}
