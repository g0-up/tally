import type { ColorCode } from "../../../shared/domain/color";
import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { MOVE_TYPE_LABELS } from "../api/adapters";
import type { QtyDisplay, StockCard, StockCardRow } from "../schemas";

export interface StockCardTableProps {
  card: StockCard;
  colorOf: (colorId: string | null | undefined) => ColorCode | "none";
  /** Auto unit mode on a SKU with several units: balances also read in the base unit. */
  showBase: boolean;
}

type ViewRow = { kind: "open" } | { kind: "move"; row: StockCardRow };

/**
 * Prototype `kho.the-kho` table: the opening balance, then every move of the period with its
 * source document, and the period totals in the footer.
 */
export function StockCardTable({ card, colorOf, showBase }: StockCardTableProps) {
  const rows: ViewRow[] = [{ kind: "open" }, ...card.rows.map((row) => ({ kind: "move" as const, row }))];
  const baseSub = (q: QtyDisplay) => (showBase && q.display !== `${num(q.qty)} ${card.base_unit}` ? <CellSub>= {`${num(q.qty)} ${card.base_unit}`}</CellSub> : null);
  const balance = (q: QtyDisplay) => (
    <>
      <b>{q.display}</b>
      {baseSub(q)}
    </>
  );
  const move = (r: ViewRow) => (r.kind === "move" ? r.row : null);
  const qtyIf = (q: QtyDisplay) => (dec(q.qty).isZero() ? "" : q.display);

  const columns: Column<ViewRow>[] = [
    { key: "date", header: "Ngày", cell: (r) => (r.kind === "open" ? "" : fd(r.row.business_date)), footer: "" },
    { key: "doc", header: "Chứng từ", cell: (r) => (r.kind === "open" ? <b>Tồn đầu kỳ</b> : <DocNo value={r.row.doc_no} />), footer: "Cộng kỳ" },
    { key: "type", header: "Loại", cell: (r) => (r.kind === "open" ? "" : MOVE_TYPE_LABELS[r.row.move_type]), footer: `${card.rows.length} dòng` },
    { key: "lot", header: "Lô", cell: (r) => (r.kind === "open" ? "" : (r.row.lot_no ?? "—")), footer: "" },
    {
      key: "color",
      header: "Màu",
      cell: (r) => {
        const m = move(r);
        if (!m) return "";
        return m.color_id ? <ColorChip color={colorOf(m.color_id)} /> : <span className="no">—</span>;
      },
      footer: "",
    },
    { key: "in", header: "Nhập", numeric: true, cell: (r) => (r.kind === "open" ? "" : qtyIf(r.row.in)), footer: card.in.display },
    { key: "out", header: "Xuất", numeric: true, cell: (r) => (r.kind === "open" ? "" : qtyIf(r.row.out)), footer: card.out.display },
    { key: "bal", header: "Tồn", numeric: true, cell: (r) => balance(r.kind === "open" ? card.opening : r.row.balance), footer: balance(card.closing) },
    { key: "cost", header: "Đơn giá vốn", numeric: true, cell: (r) => (r.kind === "open" ? "" : vnd(r.row.unit_cost)), footer: "" },
  ];
  return <DataTable caption={`Thẻ kho ${card.sku_code}`} columns={columns} rows={rows} rowKey={(r) => (r.kind === "open" ? "open" : r.row.id)} />;
}
