import { useMemo, useState } from "react";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { Button } from "../../../shared/ui/button";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { QuickProductDialog, useCategories, useColorDirectory, useSkuList, type SkuListItem } from "../../catalog";
import { EditPriceDialog, PriceHistoryTable } from "../../pricing";
import { PurchasingProductTable } from "../components/purchasing-product-table";

type Opened = { kind: "quick" } | { kind: "price"; sku: SkuListItem } | null;

const ALL = "all";

/** Goods and materials are bought; kits and combos are not listed here. */
const isBought = (s: SkuListItem) => s.product_type === "goods" || s.product_type === "material";

/** Danh sách hàng hóa (/purchasing/products). */
export default function PurchasingProductsPage() {
  const [q, setQ] = useState("");
  const [group, setGroup] = useState(ALL);
  const [opened, setOpened] = useState<Opened>(null);
  const categories = useCategories();
  const colors = useColorDirectory();
  const search = useDebounce(q.trim(), 250);
  const params = useMemo(() => ({ q: search, ...(group === ALL ? {} : { category_id: group }) }), [search, group]);
  const list = useSkuList(params);

  return (
    <>
      <PageHeader
        lead="Danh mục hàng nhập: đơn vị và quy đổi, giá nhập gần nhất, giá vốn, giá bán. Thêm hàng hóa mới hoặc chỉnh giá bán tại đây."
        actions={
          <Button variant="primary" onClick={() => setOpened({ kind: "quick" })}>
            Thêm hàng hóa mới
          </Button>
        }
      />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder="Tìm mã, tên, mã vạch…" />
        <ToggleGroup
          label="Nhóm hàng"
          value={group}
          onChange={setGroup}
          options={[{ value: ALL, label: "Tất cả nhóm" }, ...(categories.data ?? []).map((c) => ({ value: c.id, label: c.name }))]}
        />
      </FilterBar>
      {list.isLoading ? <ListLoading /> : <PurchasingProductTable rows={list.items.filter(isBought)} colors={colors} onEditPrice={(sku) => setOpened({ kind: "price", sku })} />}
      <ListFooter list={list} />
      <PriceHistoryTable skuId={null} />
      {opened?.kind === "quick" ? <QuickProductDialog onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "price" ? <EditPriceDialog sku={opened.sku} source="product_list" onClose={() => setOpened(null)} /> : null}
    </>
  );
}
