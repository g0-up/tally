import { colorLabel } from "../../../shared/domain/color";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { lineName } from "../api/adapters";
import { useColorMaps } from "../api/queries";
import type { SalesLine } from "../schemas";

export interface DocumentLinesProps<L extends SalesLine> {
  caption: string;
  lines: readonly L[];
  /** Columns after "Thành tiền" (retail price, delivered, returned…). */
  extra?: ReadonlyArray<Column<L>>;
  /** Show the "Quy cách" column (quotes). */
  spec?: boolean;
  /** Footer total of the "Thành tiền" column. */
  total?: string;
}

const LINE_TAGS: Partial<Record<SalesLine["line_kind"], string>> = { mix_pack: "Thùng mix", combo: "Combo", gift: "Quà tặng" };

/**
 * Read-only lines of a sales document (prototype `quoteLinesView` / drawer tables): the lines the
 * customer bought, a mix pack with its flavors and a combo with its colored components as sub-lines.
 */
export function DocumentLines<L extends SalesLine>({ caption, lines, extra = [], spec = false, total }: DocumentLinesProps<L>) {
  const colors = useColorMaps();
  const top = lines.filter((l) => l.parent_line_id === null);
  const childrenOf = (id: string) => lines.filter((l) => l.parent_line_id === id);
  const colorOf = (colorId: string | null) => {
    const code = colors.codeOf(colorId);
    return code === "none" ? null : colorLabel(code);
  };

  const columns: Column<L>[] = [
    {
      key: "name",
      header: "Tên sản phẩm",
      cell: (l) => {
        const color = colorOf(l.color_id);
        const tag = LINE_TAGS[l.line_kind];
        const parts = childrenOf(l.id).map((c) => {
          const cc = colorOf(c.color_id);
          return `${lineName(c)}${cc ? ` ${cc}` : ""} × ${num(c.qty)} ${c.unit_name}`;
        });
        return (
          <>
            {lineName(l)}
            {color ? ` · ${color}` : ""}
            {tag ? (
              <>
                {" "}
                <span className="tag">{tag}</span>
              </>
            ) : null}
            {parts.length > 0 ? <CellSub>{parts.join(" · ")}</CellSub> : null}
          </>
        );
      },
    },
    {
      key: "unit",
      header: "Đơn vị tính",
      cell: (l) => (
        <>
          {l.unit_name}
          {dec(l.unit_factor).gt(1) ? <CellSub>= {num(l.unit_factor)} đơn vị gốc</CellSub> : null}
        </>
      ),
    },
    { key: "qty", header: "Số lượng", numeric: true, cell: (l) => num(l.qty) },
    ...(spec ? [{ key: "spec", header: "Quy cách", cell: (l: L) => l.spec_text ?? "—" }] : []),
    { key: "price", header: "Đơn giá", numeric: true, cell: (l) => vnd(l.unit_price) },
    { key: "disc", header: "CK", numeric: true, cell: (l) => (dec(l.discount_amount).gt(0) ? vnd(l.discount_amount) : "—") },
    { key: "amount", header: "Thành tiền", numeric: true, cell: (l) => vnd(l.line_amount), ...(total !== undefined ? { footer: <b>{vnd(total)}</b> } : {}) },
    ...extra,
  ];
  if (total !== undefined && columns[0]) columns[0] = { ...columns[0], footer: "Tổng cộng" };
  return <DataTable caption={caption} columns={columns} rows={top} rowKey={(l) => l.id} flat empty="Chưa có dòng hàng." />;
}
