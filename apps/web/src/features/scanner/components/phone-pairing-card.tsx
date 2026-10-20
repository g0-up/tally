import { useEffect, useLayoutEffect, useRef, useSyncExternalStore, type ReactNode } from "react";
import { cx } from "../../../shared/lib/cx";
import { Button } from "../../../shared/ui/button";
import { pairingUrl } from "../pairing-url";
import { useScanner } from "../scanner-context";
import type { PairingSnapshot, ScannerController } from "../scanner-controller";
import { PairingQr } from "./pairing-qr";

export const PAIRING_CARD_LABEL = "Quét bằng điện thoại";
export const OFFLINE_TEXT = "Quét bằng điện thoại cần có mạng";
export const RECONNECTING_TEXT = "Đang kết nối lại…";
export const CONNECTED_TEXT = "Điện thoại đã kết nối";
export const REVOKED_TEXT = "Điện thoại đã ngắt kết nối";
export const UNSUPPORTED_TEXT = "Trình duyệt này không hỗ trợ quét bằng điện thoại.";
export const CLOSED_TEXT = "Phiên quét bằng điện thoại đã đóng vì tài khoản này vừa mở phiên ở thẻ hoặc máy khác.";
export const PAIR_AGAIN_LABEL = "Ghép lại";

/**
 * Pairs a phone as a barcode scanner for this tab and shows how the pairing stands. Showing it is
 * what opens a session; it renders nothing outside a ScannerProvider.
 */
export function PhonePairingCard({ className }: { className?: string }) {
  const scanner = useScanner();
  return scanner ? <PairingCard scanner={scanner} className={className} /> : null;
}

function PairingCard({ scanner, className }: { scanner: ScannerController; className: string | undefined }) {
  const snap = useSyncExternalStore(scanner.subscribe, scanner.getSnapshot, scanner.getSnapshot);
  useEffect(() => scanner.showCard(), [scanner]);

  // A phone asking to pair takes the focus to "Từ chối", so an Enter meant for something else (a
  // keyboard scanner's included) refuses rather than lets it in. Once answered, focus goes back to
  // where it was if it is still in the card or was dropped with the buttons.
  const cardRef = useRef<HTMLElement>(null);
  const rejectRef = useRef<HTMLButtonElement>(null);
  const before = useRef<HTMLElement | null>(null);
  const asking = snap.view.kind === "pending";
  useLayoutEffect(() => {
    if (asking) {
      const active = document.activeElement;
      before.current = active instanceof HTMLElement && !cardRef.current?.contains(active) ? active : null;
      rejectRef.current?.focus();
      return;
    }
    const back = before.current;
    before.current = null;
    const active = document.activeElement;
    const lost = active === null || active === document.body || cardRef.current?.contains(active);
    if (back && back.isConnected && lost) back.focus();
  }, [asking]);

  const { qr, status, actions } = cardParts(snap, scanner);
  const busy = snap.busy;
  // A busy command keeps both buttons focusable (aria-disabled), so the focus put on "Từ chối" is
  // not dropped to the page; the controller ignores a second command while one runs.
  const answer = asking ? (
    <>
      <Button variant="primary" size="sm" pending={busy === "approve"} pendingLabel="Đang cho phép…" aria-disabled={busy !== null || undefined} onClick={() => void scanner.approve()}>
        Cho phép
      </Button>
      <Button ref={rejectRef} size="sm" pending={busy === "reject"} pendingLabel="Đang từ chối…" aria-disabled={busy !== null || undefined} onClick={() => void scanner.reject()}>
        Từ chối
      </Button>
    </>
  ) : (
    actions
  );
  // The status line is one element for the card's whole life, at the same place, so a screen
  // reader announces each change of its text; a live region inserted with its text is often not read.
  return (
    <section ref={cardRef} className={cx("pair", asking && "pair-asking", className)} aria-label={PAIRING_CARD_LABEL}>
      {qr}
      <div className="pair-b" role="status" aria-live="polite">
        {status}
      </div>
      {answer ? <div className="pair-acts">{answer}</div> : null}
    </section>
  );
}

interface CardParts {
  /** The QR code shown before the status line. */
  qr?: ReactNode;
  status: ReactNode;
  /** The card's buttons, after the status line. */
  actions?: ReactNode;
}

function cardParts(snap: PairingSnapshot, scanner: ScannerController): CardParts {
  const { view, busy } = snap;
  switch (view.kind) {
    case "unsupported":
      return { status: UNSUPPORTED_TEXT };
    case "offline":
      return { status: OFFLINE_TEXT };
    case "inactive":
    case "creating":
      return { status: "Đang tạo mã QR…" };
    case "failed":
      return {
        status: view.message,
        actions: (
          <Button size="sm" onClick={scanner.retry}>
            Thử lại
          </Button>
        ),
      };
    case "reconnecting":
      return { status: RECONNECTING_TEXT };
    case "revoked":
      return { status: REVOKED_TEXT };
    case "replaced":
      return {
        status: "Phiên quét bằng điện thoại đang mở ở tab khác.",
        actions: (
          <Button size="sm" onClick={scanner.pairHere}>
            Ghép đôi ở tab này
          </Button>
        ),
      };
    case "closed":
      return {
        status: CLOSED_TEXT,
        actions: (
          <Button size="sm" onClick={scanner.pairHere}>
            {PAIR_AGAIN_LABEL}
          </Button>
        ),
      };
    case "waiting":
      return {
        qr: view.claimCode !== null ? <PairingQr value={pairingUrl(view.claimCode)} /> : null,
        status: (
          <>
            <b>{PAIRING_CARD_LABEL}</b>
            <span className="hint">{view.claimCode !== null ? "Mở camera điện thoại, quét mã này rồi cho phép trên máy tính." : "Đang lấy mã QR mới…"}</span>
          </>
        ),
      };
    case "pending":
      // The card adds "Cho phép" and "Từ chối" itself: it puts the focus on "Từ chối".
      return {
        status: (
          <span>
            Điện thoại{view.deviceLabel ? ` ${view.deviceLabel}` : ""} muốn kết nối. Mã xác minh <b className="pair-code">{view.verifyCode}</b>. Chỉ cho phép nếu mã trên điện
            thoại giống mã này.
          </span>
        ),
      };
    case "claimed":
      return {
        status: (
          <span>
            <b>{CONNECTED_TEXT}</b>
            {view.deviceLabel ? ` · ${view.deviceLabel}` : ""}
          </span>
        ),
        actions: (
          <Button size="sm" pending={busy === "revoke"} pendingLabel="Đang ngắt…" onClick={() => void scanner.revoke()}>
            Ngắt
          </Button>
        ),
      };
  }
}
