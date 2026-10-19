import { colorLabel } from "../../../shared/domain/color";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import { lineName, refundDue } from "../api/adapters";
import { useColorMaps, useCustomerDirectory, useReasons, useSalesDocument, useSalesReturn } from "../api/queries";
import { useSalesOverlay } from "../hooks/use-sales-overlay";
import type { SalesReturnLine } from "../schemas";
import { DrawerStatus } from "./drawer-status";

/** One sales return: the goods taken back, where they went and what it did to the customer's debt. */
export function ReturnDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const overlay = useSalesOverlay();
  const query = useSalesReturn(id);
  const customers = useCustomerDirectory();
  const colors = useColorMaps();
  const r = query.data;
  const source = useSalesDocument(r?.sales_document_id ?? null);
  const sourced = useReasons("sales_return");
  const unsourced = useReasons("return_without_source");
  const reasons = [...(sourced.data ?? []), ...(unsourced.data ?? [])];

  if (!r) {
    return (
      <DetailDrawer open onClose={onClose} title="Phiếu trả hàng" docKey={id}>
        <DrawerStatus error={query.error} deletedText="Phiếu trả hàng không còn." />
      </DetailDrawer>
    );
  }

  const columns: Column<SalesReturnLine>[] = [
    {
      key: "item",
      header: "Hàng",
      cell: (l) => {
        const code = colors.codeOf(l.color_id);
        return `${lineName(l)}${code !== "none" ? ` · ${colorLabel(code)}` : ""}`;
      },
    },
    { key: "qty", header: "SL", numeric: true, cell: (l) => `${num(l.qty)} ${l.unit_name}` },
    { key: "price", header: "Đơn giá", numeric: true, cell: (l) => vnd(l.unit_price) },
    { key: "amount", header: "Thành tiền", numeric: true, cell: (l) => vnd(l.line_amount), footer: <b>{vnd(r.total_amount)}</b> },
    { key: "to", header: "Vào kho", cell: (l) => (l.restock_to === "quarantine" ? "Khu cách ly" : "Bán được") },
  ];

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Phiếu trả hàng ${r.doc_no}`}
      sub={`${customers.name(r.customer_party_id)} · ${fd(r.business_date)}`}
      docKey={r.id}
    >
      <ButtonRow>
        <StatusPill label={r.status === "posted" ? "Đã ghi sổ" : "Đã đảo"} />
        {refundDue(r) ? (
          <Button variant="navy" size="sm" onClick={() => overlay.openDialog({ kind: "refund", salesReturn: r })}>
            Hoàn tiền
          </Button>
        ) : null}
      </ButtonRow>
      <div className="kv">
        <div>
          <b>Chứng từ gốc</b>
          <span>{r.sales_document_id ? <DocNo value={source.data?.doc_no} /> : "Không có"}</span>
        </div>
        <div>
          <b>Lý do</b>
          <span>{reasons.find((x) => x.id === r.reason_id)?.name ?? "—"}</span>
        </div>
      </div>
      <DataTable caption={`Hàng trả của ${r.doc_no}`} columns={columns} rows={r.lines ?? []} rowKey={(l) => l.id} flat empty="Chưa có dòng hàng." />
      <ImpactSummary
        items={[
          { label: "Giá trị trả", value: vnd(r.total_amount) },
          { label: "Giảm nợ khách", value: vnd(r.debt_reduction_amount) },
          { label: "Khoản cần hoàn", value: vnd(r.refund_due_amount) },
        ]}
      />
    </DetailDrawer>
  );
}
