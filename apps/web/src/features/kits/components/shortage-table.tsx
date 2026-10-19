import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { ColorDirectory } from "../../catalog";
import type { AssemblyPreview } from "../schemas";

type Line = AssemblyPreview["lines"][number];

/** Prototype `newASM` live table: what the planned kits need against the free stock. */
export function ShortageTable({ lines, colors }: { lines: readonly Line[]; colors: ColorDirectory }) {
  const columns: Column<Line>[] = [
    {
      key: "name",
      header: "Nguyên liệu",
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
    },
    { key: "required", header: "Cần", numeric: true, cell: (l) => num(l.required) },
    { key: "available", header: "Có thể bán", numeric: true, cell: (l) => num(l.available) },
    { key: "state", header: "Trạng thái", cell: (l) => (dec(l.shortage).gt(0) ? <StatusPill label="Thiếu" tone="bad" /> : <StatusPill label="Đủ" tone="ok" />) },
  ];
  return <DataTable caption="Nguyên liệu cần cho lệnh đóng" columns={columns} rows={lines} rowKey={(l, i) => `${l.sku_id}-${l.color_id ?? ""}-${i}`} flat />;
}
