import { fd, num } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { LinkButton } from "../../../shared/ui/link-button";
import { Progress } from "../../../shared/ui/progress";
import { StatusPill } from "../../../shared/ui/status-pill";
import { ASSEMBLY_STATUS_LABELS, canPostAssembly } from "../api/adapters";
import type { AssemblyOrder } from "../schemas";

export interface AssemblyTableProps {
  rows: readonly AssemblyOrder[];
  /** "SP-…" of the invoice an order was planned for, when known. */
  invoiceDoc: (invoiceId: string) => string | undefined;
  /** Components a reserved order could not hold, by order id. */
  shortages: ReadonlyMap<string, string>;
  busy: boolean;
  onOpen: (order: AssemblyOrder) => void;
  onReserve: (order: AssemblyOrder) => void;
  onComplete: (order: AssemblyOrder) => void;
  empty: string;
}

/** Prototype `gio-qua.lenh-dong`: assembly orders with their progress and the next action. */
export function AssemblyTable({ rows, invoiceDoc, shortages, busy, onOpen, onReserve, onComplete, empty }: AssemblyTableProps) {
  const columns: Column<AssemblyOrder>[] = [
    {
      key: "doc",
      header: "Lệnh",
      cell: (a) => {
        const so = a.invoice_id ? invoiceDoc(a.invoice_id) : undefined;
        return (
          <>
            <LinkButton onClick={() => onOpen(a)}>{a.doc_no}</LinkButton>
            <CellSub>
              {fd(a.planned_date ?? a.created_at)}
              {so ? ` · cho ${so}` : ""}
            </CellSub>
          </>
        );
      },
    },
    {
      key: "kit",
      header: "Giỏ",
      cell: (a) => (
        <>
          {a.kit_name}
          <CellSub>Công thức v{a.recipe_version}</CellSub>
        </>
      ),
    },
    { key: "plan", header: "Kế hoạch", numeric: true, cell: (a) => num(a.planned_qty) },
    { key: "done", header: "Hoàn thành", numeric: true, cell: (a) => num(a.completed_qty) },
    { key: "progress", header: "Tiến độ", cell: (a) => <Progress value={a.completed_qty} max={a.planned_qty} label={`Tiến độ ${a.doc_no}`} accent width={120} /> },
    {
      key: "status",
      header: "Trạng thái",
      cell: (a) => {
        const short = shortages.get(a.id);
        return (
          <>
            <StatusPill label={ASSEMBLY_STATUS_LABELS[a.status]} />
            {short ? (
              <span className="sub" style={{ color: "var(--bad-ink)" }}>
                Thiếu: {short}
              </span>
            ) : null}
          </>
        );
      },
    },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (a) =>
        a.status === "draft" ? (
          <Button variant="navy" size="sm" disabled={busy} onClick={() => onReserve(a)}>
            Chốt & giữ NL
          </Button>
        ) : canPostAssembly(a.status) ? (
          <Button variant="navy" size="sm" onClick={() => onComplete(a)}>
            Ghi nhận hoàn thành
          </Button>
        ) : null,
    },
  ];
  return <DataTable caption="Lệnh đóng giỏ" columns={columns} rows={rows} rowKey={(a) => a.id} empty={empty} />;
}
