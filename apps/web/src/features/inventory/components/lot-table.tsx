import { fd, num } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { StatusPill } from "../../../shared/ui/status-pill";
import { colorKeyOf, lotDaysLabel, lotStatusLabel } from "../api/adapters";
import type { Lot } from "../schemas";

export interface LotTableProps {
  rows: readonly Lot[];
  /** A quarantine or disposal posting is in flight; its buttons wait. */
  busy: boolean;
  onPass: (lot: Lot) => void;
  onReject: (lot: Lot) => void;
  onDispose: (lot: Lot) => void;
  onClearance: () => void;
}

/**
 * Prototype `kho.lo-han`: lots FEFO first. A quarantined lot passes back to sellable stock or is
 * destroyed, an expired lot is destroyed, a lot about to expire leads to a clearance campaign.
 */
export function LotTable({ rows, busy, onPass, onReject, onDispose, onClearance }: LotTableProps) {
  const columns: Column<Lot>[] = [
    { key: "sku", header: "Hàng", cell: (l) => `${l.sku_name}${l.variant_label ? ` · ${l.variant_label}` : ""}` },
    {
      key: "lot",
      header: "Lô",
      cell: (l) => (
        <>
          <span className="doc">{l.lot_no}</span>
          {l.color_id ? (
            <>
              {" "}
              <ColorChip color={colorKeyOf(l.color_code)} />
            </>
          ) : null}
        </>
      ),
    },
    { key: "exp", header: "Hạn dùng", cell: (l) => fd(l.expiry_date) },
    { key: "days", header: "Còn", numeric: true, cell: (l) => lotDaysLabel(l.days_to_expiry) },
    { key: "qty", header: "SL", numeric: true, cell: (l) => num(l.qty) },
    { key: "state", header: "Tình trạng", cell: (l) => <StatusPill label={lotStatusLabel(l)} /> },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (l) =>
        l.state === "quarantine" ? (
          <>
            <Button size="sm" disabled={busy} onClick={() => onPass(l)}>
              Đạt – cho bán
            </Button>{" "}
            <Button variant="danger" size="sm" disabled={busy} onClick={() => onReject(l)}>
              Không đạt – hủy
            </Button>
          </>
        ) : l.state === "expired" ? (
          <Button variant="danger" size="sm" disabled={busy} onClick={() => onDispose(l)}>
            Tiêu hủy
          </Button>
        ) : l.state === "expiring" ? (
          <Button size="sm" onClick={onClearance}>
            Xả hàng
          </Button>
        ) : null,
    },
  ];
  return <DataTable caption="Lô & hạn dùng" columns={columns} rows={rows} rowKey={(l) => `${l.id}|${l.color_id ?? ""}`} empty="Không có lô phù hợp." />;
}
