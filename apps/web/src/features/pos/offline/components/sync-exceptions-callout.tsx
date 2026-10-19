import { Button } from "../../../../shared/ui/button";
import { Callout } from "../../../../shared/ui/callout";
import type { ParkedSaleView } from "../parked-sales";

/** Offline sales the server parked: none is lost, a manager settles each in the drawer ("Xử lý"). */
export function SyncExceptionsCallout({ parked, onOpen }: { parked: readonly ParkedSaleView[]; onOpen: () => void }) {
  if (parked.length === 0) return null;
  return (
    <Callout tone="bad" role="alert" style={{ marginBottom: 16 }}>
      <b>Hàng đợi ngoại lệ ({parked.length}):</b> {parked.map((p) => `${p.label}: ${p.error}`).join(" · ")}. Giao dịch không bị mất, quản lý cần xử lý.{" "}
      <Button variant="line" size="sm" onClick={onOpen}>
        Xử lý
      </Button>
    </Callout>
  );
}
