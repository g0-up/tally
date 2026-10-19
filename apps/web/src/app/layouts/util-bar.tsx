import { useState } from "react";
import { Link } from "react-router";
import { PATHS } from "../../shared/api/endpoints";
import { useApiClient } from "../../shared/api/api-context";
import { useCommit } from "../../shared/hooks/use-commit";
import { Callout } from "../../shared/ui/callout";
import { CommitDialog } from "../../shared/ui/commit-dialog";
import { fd } from "../../shared/lib/format";
import { LogoutButton } from "../../features/auth";
import { NOTIFICATIONS_PATH } from "../menu";
import { SIDEBAR_ID } from "./sidebar";

export const SANDBOX_PILL = "Cửa hàng mẫu · dữ liệu demo";
export const SANDBOX_RESET_TITLE = "Đặt lại dữ liệu mẫu?";
export const SANDBOX_RESET_DONE = "Đã dựng lại cửa hàng mẫu.";

const strong = { color: "var(--navy-900)" } as const;

export interface UtilBarProps {
  menuOpen: boolean;
  onMenu: () => void;
  sandbox: boolean;
  tenantName: string | undefined;
  /** ISO business date. */
  businessDate: string;
  ownerName: string | undefined;
  /** Unread notifications; null while unknown. */
  unread: number | null;
}

/**
 * Prototype util bar, left to right: "☰ Menu" (narrow screens), sandbox pill, store name,
 * business date, spacer, owner, notification count, "Đặt lại mẫu" (sandbox only) and "Đăng xuất".
 * No branch selector: one store per tenant in this release.
 */
export function UtilBar({ menuOpen, onMenu, sandbox, tenantName, businessDate, ownerName, unread }: UtilBarProps) {
  const [resetOpen, setResetOpen] = useState(false);
  return (
    <header className="util">
      <button className="bell menu-btn" type="button" aria-label="Mở menu" aria-controls={SIDEBAR_ID} aria-expanded={menuOpen} onClick={onMenu}>
        ☰ Menu
      </button>
      {sandbox ? (
        <span className="sandbox" title="Dữ liệu demo không đi vào báo cáo thật">
          {SANDBOX_PILL}
        </span>
      ) : null}
      {tenantName ? <span className="tenant hide-s">{tenantName}</span> : null}
      <span className="hide-s">
        Ngày nghiệp vụ{" "}
        <b className="tab-n" style={strong}>
          {fd(businessDate)}
        </b>
      </span>
      <span className="sp" />
      {ownerName ? (
        <span className="hide-s">
          Chủ cửa hàng · <b style={strong}>{ownerName}</b>
        </span>
      ) : null}
      <Link className="bell" to={NOTIFICATIONS_PATH}>
        Việc cần xử lý <b>{unread ?? "–"}</b>
      </Link>
      {sandbox ? (
        <button className="bell" type="button" title="Đặt lại dữ liệu mẫu" onClick={() => setResetOpen(true)}>
          Đặt lại mẫu
        </button>
      ) : null}
      <LogoutButton className="bell" />
      {sandbox ? <SandboxResetDialog open={resetOpen} onClose={() => setResetOpen(false)} /> : null}
    </header>
  );
}

/** ORG-06: rebuilds the sandbox store's demo data through the API, then refreshes every screen. */
function SandboxResetDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const client = useApiClient();
  const reset = useCommit<unknown>({
    // The reset takes no Idempotency-Key. A retry that waited on the running reset finds the old
    // sandbox replaced (409 sandbox_not_active); one sent after the commit reaches the new sandbox,
    // which is too young to reset again (429).
    mutationFn: () => client.post(PATHS.sandboxReset),
    success: () => SANDBOX_RESET_DONE,
    invalidate: [[]],
    after: onClose,
  });
  return (
    <CommitDialog
      open={open}
      onClose={onClose}
      title={SANDBOX_RESET_TITLE}
      sub="Dữ liệu demo hiện tại sẽ được thay bằng dữ liệu mẫu mới; dữ liệu cũ được lưu trữ và không còn truy cập được. Thao tác này chỉ áp dụng cho sandbox."
      submitLabel="Đặt lại"
      pending={reset.pending}
      onSubmit={() => void reset.run()}
    >
      <Callout tone="warn">Dữ liệu mẫu được dựng lại bằng chính các nghiệp vụ: đầu kỳ 30/09, mua, bán, giao, thu tiền, đóng giỏ đến ngày 12/10/2026.</Callout>
    </CommitDialog>
  );
}
