import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import type { ColorDirectory } from "../../catalog";
import type { ComponentCost, CostView } from "../schemas";
import { isMaterial } from "../view-model";

export interface ComponentTableProps {
  caption: string;
  cost: CostView;
  colors: ColorDirectory;
  /** Base unit name of a component SKU. */
  unitOf: (skuId: string) => string;
}

/**
 * Prototype `compTable`: the components of a recipe at their sale price and cost; each flavor of a
 * mix line is a row tagged "mix vị N", materials are tagged "vật tư". The cost total includes the
 * packing cost.
 */
export function ComponentTable({ caption, cost, colors, unitOf }: ComponentTableProps) {
  const mixLines = [...new Set(cost.components.filter((c) => c.kind === "mix_pack").map((c) => c.line_no))];
  const packing = dec(cost.packing_cost);
  const columns: Column<ComponentCost>[] = [
    {
      key: "name",
      header: "Thành phần",
      cell: (c) => (
        <>
          {c.name}
          {c.color_id ? (
            <>
              {" "}
              <ColorChip color={colors.codeOf(c.color_id)} />
            </>
          ) : null}
          {c.kind === "mix_pack" ? <> <span className="tag">mix vị {mixLines.indexOf(c.line_no) + 1}</span></> : null}
          {isMaterial(c.kind) ? <> <span className="tag alt">vật tư</span></> : null}
        </>
      ),
      footer: "Tổng",
    },
    { key: "qty", header: "SL", numeric: true, cell: (c) => `${num(c.base_qty)} ${unitOf(c.sku_id)}` },
    { key: "price", header: "Giá bán", numeric: true, cell: (c) => (c.unit_price ? vnd(c.unit_price) : "—") },
    { key: "amount", header: "Thành tiền (giá bán)", numeric: true, cell: (c) => vnd(c.sale_amount), footer: vnd(cost.components_sale) },
    {
      key: "cost",
      header: "Giá vốn",
      numeric: true,
      cell: (c) => vnd(c.cost),
      footer: (
        <>
          {vnd(cost.total_cost)}
          {packing.gt(0) ? <CellSub>gồm đóng gói {vnd(packing)}</CellSub> : null}
        </>
      ),
    },
    { key: "sellable", header: "Có thể bán", numeric: true, cell: (c) => num(c.sellable) },
  ];
  return <DataTable caption={caption} columns={columns} rows={cost.components} rowKey={(c, i) => `${c.line_no}-${c.sku_id}-${i}`} flat />;
}
