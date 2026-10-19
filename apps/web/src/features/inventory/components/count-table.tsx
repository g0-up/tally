import { fdt } from "../../../shared/lib/dates";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { LinkButton } from "../../../shared/ui/link-button";
import { StatusPill } from "../../../shared/ui/status-pill";
import { COUNT_STATUS_LABELS } from "../api/adapters";
import type { Count, CountListItem } from "../schemas";
import { countScopeLabel } from "../view-model";

export interface CountTableProps {
  rows: readonly CountListItem[];
  categoryName: (id: string | null | undefined) => string | null;
  onOpen: (count: Count) => void;
}

/** Prototype `kho.kiem-ke`: one row per count session with its progress. */
export function CountTable({ rows, categoryName, onOpen }: CountTableProps) {
  const columns: Column<CountListItem>[] = [
    {
      key: "doc",
      header: "Phiên",
      cell: (c) => (
        <>
          <LinkButton onClick={() => onOpen(c)}>{c.doc_no}</LinkButton>
          <CellSub>Cutoff {fdt(c.started_at)}</CellSub>
        </>
      ),
    },
    { key: "scope", header: "Phạm vi", cell: (c) => countScopeLabel(c, categoryName) },
    { key: "skus", header: "Số mã", numeric: true, cell: (c) => c.line_count },
    { key: "counted", header: "Đã đếm", numeric: true, cell: (c) => c.counted_lines },
    // Variances mean nothing until counting ends, and the server hides them while a blind count is open.
    { key: "diff", header: "Dòng lệch", numeric: true, cell: (c) => (c.status === "counting" ? "—" : (c.variance_lines ?? "—")) },
    { key: "status", header: "Trạng thái", cell: (c) => <StatusPill label={COUNT_STATUS_LABELS[c.status]} /> },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (c) =>
        c.status === "counting" ? (
          <Button variant="navy" size="sm" onClick={() => onOpen(c)}>
            Nhập số đếm
          </Button>
        ) : c.status === "pending_post" ? (
          <Button variant="navy" size="sm" onClick={() => onOpen(c)}>
            Xem & ghi sổ
          </Button>
        ) : null,
    },
  ];
  return <DataTable caption="Phiên kiểm kê" columns={columns} rows={rows} rowKey={(c) => c.id} empty="Chưa có phiên kiểm kê." />;
}
