import { money, num } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ColorBreak, UnitConversionList, type ColorDirectory, type SkuListItem } from "../../catalog";
import { marginText } from "../../pricing";

export interface PurchasingProductTableProps {
  rows: readonly SkuListItem[];
  colors: ColorDirectory;
  onEditPrice: (sku: SkuListItem) => void;
}

/** Prototype `mua-hang.hang-hoa` table: units, last purchase price, cost and sell prices per SKU. */
export function PurchasingProductTable({ rows, colors, onEditPrice }: PurchasingProductTableProps) {
  const columns: Column<SkuListItem>[] = [
    {
      key: "code",
      header: "Mã",
      cell: (s) => (
        <>
          <span className="doc">{s.code}</span>
          <CellSub>{s.barcode ?? ""}</CellSub>
        </>
      ),
    },
    {
      key: "name",
      header: "Tên hàng",
      cell: (s) => (
        <div style={{ minWidth: 220 }}>
          {s.display_name}
          <CellSub>
            {s.category_name ?? ""}
            {s.product_type === "material" ? " · vật tư" : ""}
          </CellSub>
          <ColorBreak sku={s} colors={colors} />
        </div>
      ),
    },
    {
      key: "units",
      header: "Đơn vị & quy đổi",
      cell: (s) => <UnitConversionList sku={s} />,
    },
    { key: "sellable", header: "Có thể bán", numeric: true, cell: (s) => num(s.sellable) },
    { key: "last", header: "Giá nhập gần nhất", numeric: true, cell: (s) => (s.last_purchase_price === null ? "—" : money(s.last_purchase_price)) },
    { key: "cost", header: "Giá vốn BQ", numeric: true, cell: (s) => (s.avg_cost === null ? "—" : money(s.avg_cost)) },
    { key: "retail", header: "Giá bán lẻ", numeric: true, cell: (s) => (s.retail_price ? <b>{money(s.retail_price)}</b> : "—") },
    { key: "wholesale", header: "Giá sỉ", numeric: true, cell: (s) => (s.wholesale_price ? money(s.wholesale_price) : "—") },
    { key: "margin", header: "Biên lẻ", numeric: true, cell: (s) => marginText(s.retail_price, s.avg_cost) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (s) =>
        s.product_type === "goods" ? (
          <Button size="sm" onClick={() => onEditPrice(s)}>
            Sửa giá bán
          </Button>
        ) : null,
    },
  ];
  return <DataTable caption="Danh sách hàng hóa" columns={columns} rows={rows} rowKey={(s) => s.id} empty="Không có hàng phù hợp." />;
}
