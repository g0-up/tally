import { CircleCheck, PackageCheck, Pencil, Trash2 } from "lucide-react";
import { fd, money, num } from "../../../shared/lib/format";
import { Button, ButtonRow, IconButton } from "../../../shared/ui/button";
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
  /** "Sửa" and "Xóa" on a draft. */
  onEdit: (order: Order) => void;
  onDelete: (order: Order) => void;
  onReceive: (order: Order) => void;
  confirming: boolean;
}

const ICON = { size: 15, strokeWidth: 2, "aria-hidden": true } as const;

/**
 * Prototype `mua-hang.po` table: one row per PO with its receiving progress and the next step. The
 * next step ("Chốt đơn" on a draft, "Nhận hàng" on an open order) is the one filled button of a row;
 * a draft's "Sửa" and "Xóa" are quiet icon buttons before it, so the next steps line up at the right.
 */
export function PoTable({ rows, today, onOpen, onConfirm, onEdit, onDelete, onReceive, confirming }: PoTableProps) {
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
        <ButtonRow className="row-acts">
          {o.status === "draft" ? (
            <>
              <IconButton label={`Sửa ${o.doc_no}`} onClick={() => onEdit(o)}>
                <Pencil {...ICON} />
              </IconButton>
              <IconButton label={`Xóa ${o.doc_no}`} tone="danger" onClick={() => onDelete(o)}>
                <Trash2 {...ICON} />
              </IconButton>
              <Button variant="navy" size="sm" disabled={confirming} onClick={() => onConfirm(o)}>
                <CircleCheck {...ICON} />
                Chốt đơn
              </Button>
            </>
          ) : null}
          {isOpenOrder(o.status) ? (
            <Button variant="navy" size="sm" onClick={() => onReceive(o)}>
              <PackageCheck {...ICON} />
              Nhận hàng
            </Button>
          ) : null}
        </ButtonRow>
      ),
    },
  ];
  return <DataTable caption="Đơn nhập" columns={columns} rows={rows} rowKey={(o) => o.id} />;
}
