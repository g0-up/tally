import { diffDays, fdt } from "../../../shared/lib/dates";
import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { PayBox } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Timeline } from "../../../shared/ui/timeline";
import {
  DELIVERY_LABELS,
  INVOICE_STATUS_LABELS,
  PAYMENT_LABELS,
  deliveryCodeOf,
  inLineUnits,
  invoiceEditable,
  invoiceOpen,
  invoicePayment,
  lineName,
} from "../api/adapters";
import { useConfirmInvoice, useCopyInvoice, useReserveMore } from "../api/invoice-mutations";
import { useCustomerDirectory, useDebtLedger, useInvoice, useInvoiceDeliveries, useInvoiceVersions, useStockOuts } from "../api/queries";
import { useSalesOverlay } from "../hooks/use-sales-overlay";
import type { Invoice, InvoiceLine, LedgerPayment, StockOut } from "../schemas";
import { DocumentLines } from "./document-lines";
import { DrawerStatus } from "./drawer-status";
import { InvoiceActs } from "./invoice-table";

const PURPOSE_LABELS: Readonly<Record<string, string>> = { deposit: "Tiền khách trả trước", settlement: "Thanh toán công nợ" };
const PAYMENT_STATUS_LABELS: Readonly<Record<string, string>> = { posted: "Đã ghi", pending_verification: "Chờ xác minh", reversed: "Đã đảo" };

/** "Đã giao" / "Còn giao" of each line, in the unit of the line; "thiếu N" is what is not reserved yet. */
function deliveryColumns(open: boolean): Column<InvoiceLine>[] {
  return [
    { key: "delivered", header: "Đã giao", numeric: true, cell: (l) => num(inLineUnits(l.delivered_base_qty, l.unit_factor)) },
    {
      key: "remaining",
      header: "Còn giao",
      numeric: true,
      cell: (l) => {
        const remaining = dec(l.base_qty).minus(l.cancelled_base_qty).minus(l.delivered_base_qty);
        const short = open ? dec(l.backorder_base_qty) : dec(0);
        return (
          <>
            {num(inLineUnits(remaining.lte(0) ? "0" : remaining.toFixed(), l.unit_factor))}
            {short.gt(0) ? <CellSub>thiếu {num(inLineUnits(short.toFixed(), l.unit_factor))}</CellSub> : null}
          </>
        );
      },
    },
  ];
}

function stockOutColumns(lines: readonly InvoiceLine[]): Column<StockOut>[] {
  const byId = new Map(lines.map((l) => [l.id, l]));
  return [
    {
      key: "no",
      header: "Phiếu",
      cell: (so) => (
        <>
          <DocNo value={so.doc_no} />
          <CellSub>{fd(so.business_date)}</CellSub>
        </>
      ),
    },
    {
      key: "lines",
      header: "Hàng",
      cell: (so) =>
        (so.lines ?? []).map((sl) => {
          const l = byId.get(sl.invoice_line_id);
          return <div key={sl.id}>{l ? `${num(inLineUnits(sl.base_qty, l.unit_factor))} ${l.unit_name} ${lineName(l)}` : num(sl.base_qty)}</div>;
        }),
    },
    { key: "doc", header: "Chứng từ bán", cell: (so) => <DocNo value={so.sales_document?.doc_no} /> },
    { key: "status", header: "Trạng thái", cell: (so) => <StatusPill label={so.status === "posted" ? "Đã xuất kho" : "Đã đảo"} /> },
  ];
}

const PAYMENT_COLUMNS: Column<LedgerPayment>[] = [
  { key: "no", header: "Phiếu", cell: (p) => <DocNo value={p.doc_no} /> },
  { key: "kind", header: "Loại", cell: (p) => PURPOSE_LABELS[p.purpose] ?? p.purpose },
  { key: "date", header: "Ngày", cell: (p) => fd(p.business_date) },
  { key: "amount", header: "Số tiền", numeric: true, cell: (p) => vnd(p.amount) },
  { key: "status", header: "Trạng thái", cell: (p) => <StatusPill label={PAYMENT_STATUS_LABELS[p.status] ?? p.status} /> },
];

function termsText(invoice: Invoice): string {
  const days = invoice.due_date ? diffDays(invoice.due_date, invoice.invoice_date) : 0;
  return days > 0 ? `Nợ ${days} ngày từ ngày giao` : "Thanh toán ngay";
}

/** Prototype `drawSO`: an invoice with the actions its state allows, its deliveries, payments and versions. */
export function InvoiceDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const overlay = useSalesOverlay();
  const query = useInvoice(id);
  const customers = useCustomerDirectory();
  const deliveries = useInvoiceDeliveries(id);
  const versions = useInvoiceVersions(id);
  const o = query.data;
  const ledger = useDebtLedger(o?.payer_party_id);
  const stockOutIds = (deliveries.data ?? []).flatMap((d) => (d.stock_out_id ? [d.stock_out_id] : []));
  const stockOuts = useStockOuts(stockOutIds);
  const confirm = useConfirmInvoice();
  const reserve = useReserveMore();
  const copy = useCopyInvoice((c) => overlay.openDrawer({ kind: "invoice", id: c.id }));

  if (!o) {
    return (
      <DetailDrawer open onClose={onClose} title="Hóa đơn" docKey={id}>
        <DrawerStatus error={query.error} deletedText="Hóa đơn đã bị xóa." />
      </DetailDrawer>
    );
  }

  const open = invoiceOpen(o.status);
  const m = invoicePayment(o);
  const lines = o.lines ?? [];
  const paymentLabel = PAYMENT_LABELS[m.code];
  const ledgerData = ledger.data;
  const payments = ledgerData ? [...ledgerData.advances, ...ledgerData.pending_verification].filter((p) => p.earmarked_invoice_id === o.id) : [];
  const posted = stockOuts.flatMap((q) => (q.data ? [q.data] : []));

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Hóa đơn ${o.doc_no}`}
      sub={`${customers.name(o.customer_party_id)} · phiên bản ${o.current_version}${invoiceEditable(o.status) ? "" : " · đã khóa sửa/xóa"}`}
      docKey={o.id}
    >
      <ButtonRow>
        <StatusPill label={INVOICE_STATUS_LABELS[o.status]} /> <StatusPill label={DELIVERY_LABELS[deliveryCodeOf(o.status, o.delivered_amount)]} />{" "}
        <StatusPill label={paymentLabel} />
      </ButtonRow>
      <ButtonRow>
        {o.status === "draft" ? (
          <Button variant="primary" size="sm" pending={confirm.pending} onClick={() => void confirm.run(o)}>
            Xác nhận bán
          </Button>
        ) : null}
        {open ? (
          <>
            <Button variant="primary" size="sm" onClick={() => overlay.openDialog({ kind: "stockOutForm", invoice: o })}>
              Xuất kho giao khách
            </Button>
            <Button size="sm" pending={reserve.pending} onClick={() => void reserve.run(o)}>
              Giữ thêm hàng
            </Button>
            <Button variant="danger" size="sm" onClick={() => overlay.openDialog({ kind: "invoiceCancel", invoice: o })}>
              Hủy phần chưa giao
            </Button>
          </>
        ) : null}
        {dec(m.open_amount).gt(0) ? (
          <Button variant="navy" size="sm" onClick={() => overlay.openDialog({ kind: "invoiceDeposit", invoice: o })}>
            Ghi nhận thanh toán
          </Button>
        ) : null}
        <InvoiceActs
          invoice={o}
          copying={copy.pending}
          onEdit={(x) => overlay.openDialog({ kind: "invoiceForm", invoice: x })}
          onDelete={(x) => overlay.openDialog({ kind: "invoiceDelete", invoice: x })}
          onCopy={(x) => void copy.run(x)}
        />
      </ButtonRow>
      <PayBox
        items={[
          { label: "Tổng tiền", value: vnd(m.total_amount) },
          { label: "Đã thanh toán", value: vnd(m.paid_amount) },
          { label: "Còn lại", value: vnd(m.open_amount) },
          { label: "Trạng thái thanh toán", value: <StatusPill label={paymentLabel} /> },
        ]}
      />
      <div className="kv">
        <div>
          <b>Khách hàng</b>
          <span>{customers.name(o.customer_party_id)}</span>
        </div>
        <div>
          <b>Bên trả tiền</b>
          <span>{customers.name(o.payer_party_id)}</span>
        </div>
        <div>
          <b>Điều khoản</b>
          <span>{termsText(o)}</span>
        </div>
        <div>
          <b>Ngày hóa đơn</b>
          <span>{fd(o.invoice_date)}</span>
        </div>
        <div>
          <b>Ghi chú</b>
          <span>{o.note ?? "—"}</span>
        </div>
      </div>
      <h3 className="sec-h">Dòng hàng</h3>
      <DocumentLines caption={`Dòng hàng hóa đơn ${o.doc_no}`} lines={lines} extra={deliveryColumns(open)} total={o.total_amount} />
      <h3 className="sec-h">Phiếu xuất kho</h3>
      <DataTable caption={`Phiếu xuất kho của ${o.doc_no}`} columns={stockOutColumns(lines)} rows={posted} rowKey={(so) => so.id} flat empty="Chưa xuất kho lần nào." />
      <h3 className="sec-h">Thanh toán</h3>
      <DataTable caption={`Thanh toán của ${o.doc_no}`} columns={PAYMENT_COLUMNS} rows={payments} rowKey={(p) => p.id} flat empty="Chưa có khoản trả trước đang chờ trừ." />
      <p className="hint">Tiền đã trừ vào công nợ khi xuất kho được tính trong “Đã thanh toán”; chi tiết ở từng chứng từ bán.</p>
      <h3 className="sec-h">Lịch sử</h3>
      <Timeline
        items={(versions.data?.data ?? []).map((v) => ({
          key: v.id,
          time: fdt(v.created_at),
          content: `Phiên bản ${v.version_no}${v.change_summary ? ` · ${v.change_summary}` : ""}`,
        }))}
      />
    </DetailDrawer>
  );
}
