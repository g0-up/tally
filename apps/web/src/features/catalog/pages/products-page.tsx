import { useState } from "react";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { num, vnd } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button } from "../../../shared/ui/button";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { PRODUCT_TYPE_LABELS, SKU_STATUS_LABELS, unitConversionLines } from "../api/adapters";
import { useColorDirectory, useSkuList, type ColorDirectory } from "../api/queries";
import { ColorBreak } from "../components/color-break";
import { BarcodeUnitsDrawer } from "../containers/barcode-units-drawer";
import { NewProductDialog } from "../containers/new-product-dialog";
import type { SkuListItem } from "../schemas";

const NAME_CELL = { minWidth: 220 } as const;

const priceOrDash = (v: string | null) => (v === null ? "—" : vnd(v));

function productColumns(colors: ColorDirectory, onBarcodes: (skuId: string) => void): Column<SkuListItem>[] {
  return [
    {
      key: "code",
      header: "Mã · mã vạch",
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
        <div style={NAME_CELL}>
          {s.display_name}
          <CellSub>{s.category_name ?? ""}</CellSub>
          {s.product_type === "combo" ? null : <ColorBreak sku={s} colors={colors} />}
        </div>
      ),
    },
    {
      key: "type",
      header: "Loại",
      cell: (s) => (
        <>
          <Badge alt={s.product_type !== "goods"}>{PRODUCT_TYPE_LABELS[s.product_type]}</Badge>
          {s.in_mix_group ? (
            <>
              {" "}
              <Badge alt>mix vị</Badge>
            </>
          ) : null}
        </>
      ),
    },
    {
      key: "units",
      header: "Đơn vị & quy đổi",
      cell: (s) =>
        unitConversionLines(s).map((line, i) => (
          <span key={line}>
            {i > 0 ? <br /> : null}
            {line}
          </span>
        )),
    },
    { key: "lot", header: "Lô/hạn", cell: (s) => (s.track_lot ? "Bắt buộc" : "Không") },
    { key: "retail", header: "Giá lẻ", numeric: true, cell: (s) => priceOrDash(s.retail_price) },
    { key: "wholesale", header: "Giá sỉ", numeric: true, cell: (s) => priceOrDash(s.wholesale_price) },
    { key: "cost", header: "Giá vốn BQ", numeric: true, cell: (s) => priceOrDash(s.avg_cost) },
    { key: "min-price", header: "Giá tối thiểu", numeric: true, cell: (s) => priceOrDash(s.min_price) },
    { key: "min-stock", header: "Tồn tối thiểu", numeric: true, cell: (s) => num(s.min_stock) },
    { key: "status", header: <span className="sr-only">Trạng thái</span>, cell: (s) => <StatusPill label={SKU_STATUS_LABELS[s.status]} /> },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (s) => (
        <Button size="sm" aria-label={`Mã vạch ${s.display_name}`} onClick={() => onBarcodes(s.id)}>
          Mã vạch
        </Button>
      ),
    },
  ];
}

/** Hàng hóa (/catalog/products). */
export default function ProductsPage() {
  const [q, setQ] = useState("");
  const [adding, setAdding] = useState(false);
  const [barcodesOf, setBarcodesOf] = useState<string | null>(null);
  const query = useDebounce(q.trim(), 250);
  const list = useSkuList({ q: query });
  const colors = useColorDirectory();
  // The drawer reads the SKU from the list on every render, so a refetch (units added elsewhere)
  // reaches it; a SKU that left the list closes it for good rather than reopening when it returns.
  const barcodesSku = barcodesOf === null ? undefined : list.items.find((s) => s.id === barcodesOf);
  if (barcodesOf !== null && barcodesSku === undefined) setBarcodesOf(null);

  return (
    <>
      <PageHeader
        lead="Mỗi SKU có đơn vị cơ sở và hệ số quy đổi. Hệ số được chụp trên từng dòng chứng từ; đổi hệ số không sửa lịch sử. Giá mua tham khảo không tự thành giá vốn."
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            Thêm hàng hóa
          </Button>
        }
      />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder="Tìm mã, tên, mã vạch…" />
      </FilterBar>
      {list.isLoading ? <ListLoading /> : <DataTable caption="Hàng hóa" columns={productColumns(colors, setBarcodesOf)} rows={list.items} rowKey={(s) => s.id} />}
      <ListFooter list={list} />
      {adding ? <NewProductDialog onClose={() => setAdding(false)} /> : null}
      {barcodesSku ? <BarcodeUnitsDrawer sku={barcodesSku} onClose={() => setBarcodesOf(null)} /> : null}
    </>
  );
}
