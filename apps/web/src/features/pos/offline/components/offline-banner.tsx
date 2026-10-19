import { useId, useState } from "react";
import { fdt } from "../../../../shared/lib/dates";
import { Button } from "../../../../shared/ui/button";
import { Callout } from "../../../../shared/ui/callout";
import { bundleFresh } from "../offline-bundle-repository";
import type { OfflineBundle, OutboxEntry } from "../offline-db";
import { OFFLINE_MESSAGES } from "../offline-sale-service";
import { PendingSyncList } from "./pending-sync-list";

export interface OfflineBannerProps {
  /** Catalog this device sells from; null when none was downloaded or the store does not sell offline. */
  bundle: OfflineBundle | null;
  pending: readonly OutboxEntry[];
  now?: Date;
}

/** What the counter can do without the network, the price time it sells at, and what waits to sync. */
function OfflineRules({ bundle, now }: { bundle: OfflineBundle | null; now: Date }) {
  if (!bundle) return <>{OFFLINE_MESSAGES.noBundle}</>;
  if (!bundle.settings.offline_pos_enabled) return <>{OFFLINE_MESSAGES.disabled}</>;
  if (!bundle.settings.prices_include_tax) return <>{OFFLINE_MESSAGES.taxExcluded}</>;
  return (
    <>
      Dùng bảng giá cache lúc {fdt(bundle.cached_at)}. Chỉ bán thu tiền ngay; không bán chịu, không hoàn tiền. Tồn hiển thị có thể chưa chính xác.
      {bundleFresh(bundle, now) ? null : <> {OFFLINE_MESSAGES.expired}</>}
    </>
  );
}

/**
 * Warn banner of the POS while the API cannot be reached; "Xem giao dịch chờ" lists the outbox.
 * It is announced by the live region it is placed in (`ConnectionStatus`).
 */
export function OfflineBanner({ bundle, pending, now = new Date() }: OfflineBannerProps) {
  const [open, setOpen] = useState(false);
  const listId = useId();
  const n = pending.length;
  return (
    <>
      <Callout tone="warn" style={{ marginBottom: 16 }}>
        <b>Đang ngoại tuyến.</b> <OfflineRules bundle={bundle} now={now} /> Giao dịch chờ đồng bộ: <b>{n}</b>.
        {n > 0 ? (
          <>
            {" "}
            <Button variant="line" size="sm" aria-expanded={open} aria-controls={listId} onClick={() => setOpen((v) => !v)}>
              Xem giao dịch chờ
            </Button>
          </>
        ) : null}
      </Callout>
      {open && n > 0 ? <PendingSyncList id={listId} entries={pending} /> : null}
    </>
  );
}
