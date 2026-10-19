import { fd, money, num } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { LinkButton } from "../../../shared/ui/link-button";
import { Progress } from "../../../shared/ui/progress";
import { StatusPill } from "../../../shared/ui/status-pill";
import { ORDER_STATUS_LABELS, isLate, isOpenOrder } from "../api/adapters";
import type { Order } from "../schemas";

export interface PoTableProps {
  rows: readonly Order[];
  /** Business date: an open order expected before it is "Quá hạn giao". */
  today: string;
  onOpen: (order: Order) => void;
  onConfirm: (order: Order) => void;
  onReceive: (order: Order) => void;
  confirming: boolean;
}

/** Prototype `mua-hang.po` table: one row per PO with its receiving progress and the next step. */
export function PoTable({ rows, today, onOpen, onConfirm, onReceive, confirming }: PoTableProps) {
  const columns: Column<Order>[] = [
    {
      key: "doc",
      header: "Đơn nhập",
      cell: (o) => (
        <>
          <LinkButton onClick={() => onOpen(o)}>{o.doc_no}</LinkButton>
          <CellSub>{fd(o.order_date)}</CellSub>
        </>
      ),
    },
    { key: "supplier", header: "Nhà cung cấp", cell: (o) => o.supplier_name },
    {
      key: "eta",
      header: "Dự kiến giao",
      cell: (o) => (
        <>
          {fd(o.expected_date)}
          {isLate(o, today) ? (
            <>
              {" "}
              <StatusPill label="Quá hạn giao" />
            </>
          ) : null}
        </>
      ),
    },
    { key: "total", header: "Giá trị", numeric: true, cell: (o) => money(o.total_amount) },
    {
      key: "progress",
      header: "Tiến độ nhận",
      cell: (o) => {
        const total = o.ordered_base_qty ?? "0";
        const received = o.received_base_qty ?? "0";
        return (
          <>
            <Progress value={received} max={total} label={`Tiến độ nhận ${o.doc_no}`} width={120} />
            <CellSub>
              {num(received)} / {num(total)} đơn vị cơ sở
            </CellSub>
          </>
        );
      },
    },
    { key: "status", header: "Trạng thái", cell: (o) => <StatusPill label={ORDER_STATUS_LABELS[o.status]} /> },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (o) => (
        <ButtonRow style={{ flexWrap: "nowrap", whiteSpace: "nowrap" }}>
          {o.status === "draft" ? (
            <Button size="sm" disabled={confirming} onClick={() => onConfirm(o)}>
              Chốt đơn
            </Button>
          ) : null}
          {isOpenOrder(o.status) ? (
            <Button variant="navy" size="sm" onClick={() => onReceive(o)}>
              Nhận hàng
            </Button>
          ) : null}
        </ButtonRow>
      ),
    },
  ];
  return <DataTable caption="Đơn nhập" columns={columns} rows={rows} rowKey={(o) => o.id} />;
}
