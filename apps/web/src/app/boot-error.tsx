import { Button } from "../shared/ui/button";
import { BRAND_NAME, BRAND_TAGLINE } from "./layouts/brand";

export const BOOT_OFFLINE = "Không kết nối được máy chủ. Kiểm tra mạng rồi thử lại.";
export const BOOT_MISDIRECTED = "Địa chỉ này không thuộc hệ thống. Kiểm tra lại đường dẫn cửa hàng.";

/** Shown when the host context cannot be read before the router exists. */
export function BootError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const misdirected = error instanceof Error && error.message === "misdirected_request";
  return (
    <div className="auth">
      <div className="auth-card">
        <div className="auth-brand">
          <b>{BRAND_NAME}</b>
          <span>{BRAND_TAGLINE}</span>
        </div>
        <div className="auth-body" role="alert">
          <p>{misdirected ? BOOT_MISDIRECTED : BOOT_OFFLINE}</p>
          {misdirected ? null : <Button onClick={onRetry}>Thử lại</Button>}
        </div>
      </div>
    </div>
  );
}
