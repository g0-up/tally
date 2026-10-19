import { Button } from "../../../../shared/ui/button";
import { Callout } from "../../../../shared/ui/callout";

/**
 * Online again with offline sales still on the device; sync runs by itself, the button retries now.
 * It is announced by the live region it is placed in (`ConnectionStatus`).
 */
export function SyncQueueCallout({ count, syncing, onSync }: { count: number; syncing: boolean; onSync: () => void }) {
  if (count === 0) return null;
  return (
    <Callout style={{ marginBottom: 16 }}>
      Có {count} giao dịch ngoại tuyến chờ đồng bộ.{" "}
      <Button variant="navy" size="sm" pending={syncing} pendingLabel="Đang đồng bộ…" onClick={onSync}>
        Đồng bộ ngay
      </Button>
    </Callout>
  );
}
