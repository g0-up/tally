import { useMemo, useState } from "react";
import { COLORS, isColorCode } from "../../../shared/domain/color";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { Button } from "../../../shared/ui/button";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { useCategories, useColorDirectory, useSkusByIds, type SkuListItem } from "../../catalog";
import { EditPriceDialog } from "../../pricing";
import { colorKeyOf } from "../api/adapters";
import { colorPriceKey, useColorPrices, useStockList } from "../api/queries";
import { StockTable } from "../components/stock-table";
import { hasColors, stockColors, type ColorFilter } from "../view-model";
import { AdjustmentDialog } from "../containers/adjustment-dialog";
import { ColorPriceDialog } from "../containers/color-price-dialog";
import type { ColorStock, StockRow } from "../schemas";
import { useSeenIds } from "../use-seen-ids";
import { useUrlFilters } from "../use-url-filters";

type Opened = { kind: "adjust" } | { kind: "price"; sku: SkuListItem } | { kind: "color"; sku: SkuListItem; color: ColorStock } | null;

const toFilter = (v: string | null): ColorFilter => (v === "none" || isColorCode(v) ? v : "all");

/** Tồn kho (/inventory/stock). Filters live in the URL: `q`, `cat`, `color`, `only=co`. */
export default function StockPage() {
  const url = useUrlFilters();
  const q = url.get("q") ?? "";
  const categoryId = url.get("cat");
  const pick = toFilter(url.get("color"));
  const onlyColored = url.get("only") === "co";
  const search = useDebounce(q);
  const categories = useCategories();
  const colors = useColorDirectory();
  // The server filters by color, untagged stock and "colored only", but takes untagged stock with
  // neither of the others: "Chỉ hàng có màu" with "Chưa gán màu" narrows the loaded rows here.
  const colorId = isColorCode(pick) ? colors.idOf(pick) : null;
  const uncolored = pick === "none";
  const list = useStockList({ q: search, categoryId, colorId, coloredOnly: onlyColored && !uncolored, uncolored });
  // A row toggled by hand keeps its state; changing the color filter forgets it.
  const [open, setOpen] = useState<{ pick: ColorFilter; rows: Record<string, boolean> }>({ pick, rows: {} });
  const openRows = open.pick === pick ? open.rows : {};
  const [opened, setOpened] = useState<Opened>(null);

  const all = list.items;
  // Re-checked here for that pair and for a color whose id the directory has not resolved yet.
  const rows = all.filter((r) => (!onlyColored || hasColors(r)) && (pick === "all" || stockColors(r).some((c) => colorKeyOf(c.color_code) === pick)));
  const loadedOnly = uncolored && onlyColored && list.hasMore;
  const skuIds = useMemo(() => all.map((r) => r.sku_id), [all]);
  const skus = useSkusByIds(skuIds);
  // Color prices cost two requests per SKU, so only the rows shown so far load them.
  const { seen, markSeen } = useSeenIds();
  const pricedIds = useMemo(
    () => all.filter((r) => seen.has(r.sku_id) && hasColors(r) && skus.data?.get(r.sku_id)?.has_color_prices).map((r) => r.sku_id),
    [all, seen, skus.data],
  );
  const colorPrices = useColorPrices(pricedIds);

  const anyColored = rows.some(hasColors);

  const toggle = (skuId: string, next: boolean) => setOpen({ pick, rows: { ...openRows, [skuId]: next } });
  const toggleAll = (next: boolean) => setOpen({ pick, rows: Object.fromEntries(rows.filter(hasColors).map((r) => [r.sku_id, next])) });
  const colorPrice = (row: StockRow, color: ColorStock) => {
    const sku = skus.data?.get(row.sku_id);
    if (sku) setOpened({ kind: "color", sku, color });
  };

  return (
    <>
      <PageHeader
        lead={
          <>
            Hàng có màu hiện số tồn từng màu ngay trên dòng; bấm ▸ để xem chi tiết và sửa giá riêng từng màu. <b>Có thể bán</b> = tồn thực − hàng cách ly/hết hạn − hàng đang giữ cho hóa đơn (ghi nhỏ bên dưới khi có).
          </>
        }
        actions={
          <Button variant="primary" onClick={() => setOpened({ kind: "adjust" })}>
            Điều chỉnh kho
          </Button>
        }
      />
      <FilterBar>
        <SearchInput value={q} onChange={(v) => url.set({ q: v })} placeholder="Tìm mã, tên, mã vạch…" />
        <ToggleGroup
          label="Nhóm hàng"
          options={[{ value: "all", label: "Tất cả nhóm" }, ...(categories.data ?? []).map((c) => ({ value: c.id, label: c.name }))]}
          value={categoryId ?? "all"}
          onChange={(v) => url.set({ cat: v === "all" ? null : v })}
        />
      </FilterBar>
      <FilterBar>
        <span className="hint">Màu:</span>
        <ToggleGroup<ColorFilter>
          label="Màu"
          options={[
            { value: "all", label: "Mọi màu" },
            ...COLORS.filter((c) => c.key === pick || colors.idOf(c.key) !== null).map((c) => ({ value: c.key, label: c.label })),
            { value: "none", label: "Chưa gán màu" },
          ]}
          value={pick}
          onChange={(v) => url.set({ color: v === "all" ? null : v })}
        />
        <ToggleGroup
          label="Phạm vi"
          options={[
            { value: "tat-ca", label: "Tất cả hàng" },
            { value: "co", label: "Chỉ hàng có màu" },
          ]}
          value={onlyColored ? "co" : "tat-ca"}
          onChange={(v) => url.set({ only: v === "co" ? "co" : null })}
        />
        {anyColored ? (
          <>
            <span style={{ flex: 1 }} aria-hidden="true" />
            <Button onClick={() => toggleAll(true)}>Mở tất cả màu</Button>
            <Button onClick={() => toggleAll(false)}>Thu gọn tất cả</Button>
          </>
        ) : null}
      </FilterBar>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <StockTable
          rows={rows}
          skus={skus.data}
          colorPrices={colorPrices}
          onSeen={markSeen}
          partial={list.hasMore}
          pick={pick}
          open={openRows}
          onToggle={toggle}
          onEditPrice={(sku) => setOpened({ kind: "price", sku })}
          onColorPrice={colorPrice}
        />
      )}
      {loadedOnly ? <p className="hint">“Chỉ hàng có màu” cùng “Chưa gán màu” chỉ lọc trên các dòng đã tải; bấm “Xem thêm” để lọc tiếp.</p> : null}
      <ListFooter list={list} />
      <p className="hint">Combo ghép tại quầy không có tồn riêng; lượng có thể bán tính theo thành phần.</p>
      {opened?.kind === "adjust" ? <AdjustmentDialog onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "price" ? <EditPriceDialog sku={opened.sku} source="inventory" onClose={() => setOpened(null)} /> : null}
      {opened?.kind === "color" ? (
        <ColorPriceDialog sku={opened.sku} color={opened.color} own={colorPrices.get(colorPriceKey(opened.sku.id, opened.color.color_id))} onClose={() => setOpened(null)} />
      ) : null}
    </>
  );
}
