import { useMemo } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { fd, money, num } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { LIST_LOADING_TEXT } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { baseUnitName, useColorDirectory, useSkusByIds } from "../../catalog";
import { ORDER_STATUS_LABELS, isOpenOrder, receiptOpenAmount } from "../api/adapters";
import { useConfirmOrder } from "../api/mutations";
import { useOrder, useOrderReceipts, usePartyAdvances, useReasons } from "../api/queries";
import type { Order, OrderLine, Payment, ReceiptListItem } from "../schemas";

export interface PoDrawerProps {
  id: string;
  onClose: () => void;
  onReceive: (order: Order) => void;
  onAdvance: (order: Order) => void;
  onCloseShort: (order: Order) => void;
  /** "Sửa" and "Xóa nháp" on a draft. */
  onEdit: (order: Order) => void;
  onDelete: (order: Order) => void;
}

/** Prototype `openPO`: the order, its lines with what was received, its receipts and advances. */
export function PoDrawer({ id, onClose, onReceive, onAdvance, onCloseShort, onEdit, onDelete }: PoDrawerProps) {
  const order = useOrder(id);
  const o = order.data;
  const receipts = useOrderReceipts(id);
  const advances = usePartyAdvances(o?.supplier_party_id ?? null);
  const reasons = useReasons("close_short");
  const confirm = useConfirmOrder();
  const colors = useColorDirectory();
  const lines = useMemo(() => o?.lines ?? [], [o]);
  const skus = useSkusByIds(useMemo(() => lines.map((l) => l.sku_id), [lines]));

  if (!o) {
    return (
      <DetailDrawer open onClose={onClose} title="Đơn nhập" docKey={id}>
        {order.error ? <DrawerError message={errorMessage(order.error)} /> : <p className="hint">{LIST_LOADING_TEXT}</p>}
      </DetailDrawer>
    );
  }

  const open = isOpenOrder(o.status);
  const closeReason = o.close_reason_id ? reasons.data?.find((r) => r.id === o.close_reason_id)?.name : undefined;
  const earmarked = (advances.data ?? []).filter((p) => p.source_type === "purchase" && p.source_id === o.id);

  const lineColumns: Column<OrderLine>[] = [
    {
      key: "name",
      header: "Tên sản phẩm",
      cell: (l) => (
        <>
          {l.name}
          {l.color_id ? (
            <>
              {" "}
              <ColorChip color={colors.codeOf(l.color_id)} />
            </>
          ) : null}
        </>
      ),
      footer: "Tổng",
    },
    {
      key: "unit",
      header: "Đơn vị tính",
      cell: (l) => {
        const sku = skus.data?.get(l.sku_id);
        return (
          <>
            {l.unit_name}
            {dec(l.unit_factor).gt(1) ? (
              <CellSub>
                = {num(l.unit_factor)} {sku ? baseUnitName(sku) : ""}
              </CellSub>
            ) : null}
          </>
        );
      },
    },
    { key: "qty", header: "Số lượng", numeric: true, cell: (l) => num(l.qty) },
    { key: "spec", header: "Quy cách", cell: (l) => l.spec_text || "—" },
    { key: "price", header: "Đơn giá", numeric: true, cell: (l) => money(l.unit_price) },
    { key: "amount", header: "Thành tiền", numeric: true, cell: (l) => money(l.line_amount), footer: money(o.total_amount) },
    { key: "received", header: "Đã nhận", numeric: true, cell: (l) => num(l.received_base_qty) },
    { key: "pending", header: "Còn chờ", numeric: true, cell: (l) => (o.status === "closed_short" ? "0 (đã đóng)" : num(l.pending_base_qty)) },
  ];

  const receiptColumns: Column<ReceiptListItem>[] = [
    { key: "doc", header: "Phiếu nhận", cell: (r) => <DocNo value={r.doc_no} /> },
    { key: "date", header: "Ngày", cell: (r) => fd(r.business_date) },
    { key: "invoice", header: "Hóa đơn NCC", cell: (r) => r.supplier_document_no ?? "Chưa đối chiếu" },
    { key: "amount", header: "Giá trị", numeric: true, cell: (r) => money(r.total_amount) },
    { key: "open", header: "Còn phải trả", numeric: true, cell: (r) => money(receiptOpenAmount(r)) },
  ];

  const advanceColumns: Column<Payment>[] = [
    { key: "doc", header: "Phiếu chi", cell: (p) => <DocNo value={p.doc_no} /> },
    { key: "amount", header: "Số tiền", numeric: true, cell: (p) => money(p.amount) },
    { key: "allocated", header: "Đã phân bổ", numeric: true, cell: (p) => money(p.allocated_amount ?? "0") },
  ];

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Đơn nhập ${o.doc_no}`}
      sub={`${o.supplier_name} · tạo ${fd(o.order_date)} · dự kiến giao ${fd(o.expected_date)}`}
      docKey={o.id}
    >
      <ButtonRow>
        <StatusPill label={ORDER_STATUS_LABELS[o.status]} /> {closeReason ? <span className="hint">{closeReason}</span> : null}
      </ButtonRow>
      <ButtonRow>
        {open ? (
          <>
            <Button variant="primary" size="sm" onClick={() => onReceive(o)}>
              Nhận hàng
            </Button>
            <Button size="sm" onClick={() => onAdvance(o)}>
              Ứng trước NCC
            </Button>
            <Button variant="danger" size="sm" onClick={() => onCloseShort(o)}>
              Đóng phần còn thiếu
            </Button>
          </>
        ) : null}
        {o.status === "draft" ? (
          <>
            <Button variant="primary" size="sm" pending={confirm.pending} onClick={() => void confirm.run({ id: o.id, docNo: o.doc_no, version: o.version })}>
              Chốt đơn
            </Button>
            <Button variant="navy" size="sm" onClick={() => onEdit(o)}>
              Sửa
            </Button>
            <Button variant="danger" size="sm" onClick={() => onDelete(o)}>
              Xóa nháp
            </Button>
          </>
        ) : null}
      </ButtonRow>
      <DataTable caption={`Dòng hàng ${o.doc_no}`} columns={lineColumns} rows={lines} rowKey={(l) => l.id} flat />
      <h3 className="sec-h">Phiếu nhận &amp; nợ NCC</h3>
      <DataTable caption="Phiếu nhận & nợ NCC" columns={receiptColumns} rows={receipts.data ?? []} rowKey={(r) => r.id} flat empty="Chưa nhận hàng." />
      {earmarked.length > 0 ? (
        <>
          <h3 className="sec-h">Ứng trước NCC</h3>
          <DataTable caption="Ứng trước NCC" columns={advanceColumns} rows={earmarked} rowKey={(p) => p.id} flat />
          <p className="hint">Tổng ứng trước {money(sumDec(earmarked.map((p) => p.amount)))}</p>
        </>
      ) : null}
    </DetailDrawer>
  );
}
