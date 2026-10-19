import { colorLabel } from "../../../shared/domain/color";
import { fd, num, vnd } from "../../../shared/lib/format";
import { ButtonRow } from "../../../shared/ui/button";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import { inLineUnits, lineName } from "../api/adapters";
import { useColorMaps, useCustomerDirectory, useInvoice, useStockOut, useStockOutLots } from "../api/queries";
import type { InvoiceLine, StockOut } from "../schemas";
import { DrawerStatus } from "./drawer-status";

type StockOutLine = NonNullable<StockOut["lines"]>[number];

/** The lots one stock-out line took ("L2409 · 30/06/2027"). */
function LotsCell({ stockOut, line }: { stockOut: StockOut; line: StockOutLine }) {
  const lots = useStockOutLots(stockOut.id, line.sku_id, stockOut.business_date);
  const colors = useColorMaps();
  if (lots.isPending)
    return (
      <span className="hint">
        <span aria-hidden="true">…</span>
        <span className="sr-only">Đang tải</span>
      </span>
    );
  const moves = (lots.data ?? []).filter((m) => line.color_id === null || m.color_id === line.color_id);
  if (moves.length === 0) return <span className="hint">Chưa có dữ liệu lô</span>;
  return moves.map((m) => {
    const code = colors.codeOf(m.color_id);
    return (
      <div key={m.id}>
        {m.lot_no}
        {m.expiry_date ? ` · ${fd(m.expiry_date)}` : ""}
        {code !== "none" ? ` · ${colorLabel(code)}` : ""}
      </div>
    );
  });
}

/** Prototype `drawDLV`: one stock-out, the lots it took and the receivable it raised. */
export function StockOutDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const query = useStockOut(id);
  const so = query.data;
  const invoice = useInvoice(so?.invoice_id ?? null);
  const customers = useCustomerDirectory();
  const colors = useColorMaps();

  if (!so) {
    return (
      <DetailDrawer open onClose={onClose} title="Phiếu xuất kho" docKey={id}>
        <DrawerStatus error={query.error} deletedText="Phiếu xuất kho không còn." />
      </DetailDrawer>
    );
  }

  const byId = new Map<string, InvoiceLine>((invoice.data?.lines ?? []).map((l) => [l.id, l]));
  const doc = so.sales_document;
  const columns: Column<StockOutLine>[] = [
    {
      key: "item",
      header: "Hàng",
      cell: (sl) => {
        const l = byId.get(sl.invoice_line_id);
        const code = colors.codeOf(sl.color_id);
        return `${l ? lineName(l) : "…"}${code !== "none" ? ` · ${colorLabel(code)}` : ""}`;
      },
    },
    {
      key: "qty",
      header: "SL",
      numeric: true,
      cell: (sl) => {
        const l = byId.get(sl.invoice_line_id);
        return l ? `${num(inLineUnits(sl.base_qty, l.unit_factor))} ${l.unit_name}` : num(sl.base_qty);
      },
    },
    { key: "lots", header: "Lô xuất", cell: (sl) => <LotsCell stockOut={so} line={sl} /> },
  ];

  return (
    <DetailDrawer open onClose={onClose} title={`Phiếu xuất kho ${so.doc_no}`} sub={`Hóa đơn ${invoice.data?.doc_no ?? "…"} · ${fd(so.business_date)}`} docKey={so.id}>
      <ButtonRow>
        <StatusPill label={so.status === "posted" ? "Đã xuất kho" : "Đã đảo"} />
      </ButtonRow>
      <div className="kv">
        <div>
          <b>Khách hàng</b>
          <span>{customers.name(doc?.customer_party_id ?? invoice.data?.customer_party_id)}</span>
        </div>
        <div>
          <b>Chứng từ bán</b>
          <span>{doc ? <DocNo value={doc.doc_no} /> : "Chưa phát sinh"}</span>
        </div>
      </div>
      <DataTable caption={`Hàng xuất của ${so.doc_no}`} columns={columns} rows={so.lines ?? []} rowKey={(l) => l.id} flat />
      {doc ? (
        <ImpactSummary
          items={[
            { label: "Phải thu phát sinh", value: vnd(doc.payment.total_amount) },
            { label: "Đã thanh toán", value: vnd(doc.payment.paid_amount) },
            { label: "Khách còn nợ", value: vnd(doc.payment.open_amount) },
          ]}
        />
      ) : null}
      {so.note ? <p className="hint">{so.note}</p> : null}
    </DetailDrawer>
  );
}
