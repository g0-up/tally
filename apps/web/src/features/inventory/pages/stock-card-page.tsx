import { useMemo } from "react";
import { colorLabel, COLORS, isColorCode, type ColorKey } from "../../../shared/domain/color";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { isIsoDate, monthStart } from "../../../shared/lib/dates";
import { dec } from "../../../shared/lib/decimal";
import { fd } from "../../../shared/lib/format";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useColorDirectory, useSkusByIds } from "../../catalog";
import { colorKeyOf } from "../api/adapters";
import { useStockCard, useStockDetail, type StockCardParams } from "../api/queries";
import { StockCardFilters, type StockCardFilterValue } from "../components/stock-card-filters";
import { StockCardKpis } from "../components/stock-card-kpis";
import { StockCardTable } from "../components/stock-card-table";
import type { ColorFilter } from "../view-model";
import { useUrlFilters } from "../use-url-filters";

const toFilter = (v: string | null): ColorFilter => (v === "none" || isColorCode(v) ? v : "all");
const isoOr = (v: string | null, fallback: string) => (v !== null && isIsoDate(v) ? v : fallback);

/** Thẻ kho (/inventory/stock-card). Filters live in the URL: `sku`, `color`, `from`, `to`, `unit`. */
export default function StockCardPage() {
  const url = useUrlFilters();
  const today = useBusinessDate();
  const skuId = url.get("sku");
  const a = isoOr(url.get("from"), monthStart(today));
  const b = isoOr(url.get("to"), today);
  const [from, to] = a > b ? [b, a] : [a, b];
  const skus = useSkusByIds(useMemo(() => (skuId === null ? [] : [skuId]), [skuId]));
  const sku = skuId === null ? undefined : skus.data?.get(skuId);
  const unitId = sku?.units.some((u) => u.unit_id === url.get("unit")) ? url.get("unit") : null;
  const detail = useStockDetail(skuId);
  const colorDir = useColorDirectory();

  const stockColors = detail.data?.sku_id === skuId ? (detail.data?.colors ?? []) : [];
  const colors: ColorKey[] = [...new Set(stockColors.map((c) => colorKeyOf(c.color_code)))];
  const wanted = toFilter(url.get("color"));
  const color: ColorFilter = wanted === "all" || colors.includes(wanted) ? wanted : "all";
  const colorId = color === "all" || color === "none" ? null : colorDir.idOf(color);

  const params: StockCardParams | null =
    skuId === null || (color !== "all" && color !== "none" && colorId === null) ? null : { skuId, colorId, uncolored: color === "none", from, to, unitId };
  const card = useStockCard(params);
  const data = card.card;
  const multiUnit = (sku?.units.filter((u) => u.active).length ?? 0) > 1;
  const moves = data && !card.hasMore ? { in: data.rows.filter((r) => dec(r.in.qty).gt(0)).length, out: data.rows.filter((r) => dec(r.out.qty).gt(0)).length } : null;
  const colorName = color === "all" ? "" : color === "none" ? " · chưa gán màu" : ` · màu ${colorLabel(color).toLowerCase()}`;

  const change = (patch: Partial<StockCardFilterValue>) =>
    url.set({
      ...(patch.from !== undefined ? { from: patch.from } : {}),
      ...(patch.to !== undefined ? { to: patch.to } : {}),
      ...(patch.color !== undefined ? { color: patch.color === "all" ? null : patch.color } : {}),
      ...(patch.unitId !== undefined ? { unit: patch.unitId } : {}),
    });

  return (
    <>
      <PageHeader lead="Sổ nhập – xuất – tồn của một mặt hàng theo thời gian. Mỗi dòng có chứng từ gốc; bấm số chứng từ để xem. Dòng đầu là tồn đầu kỳ, cột Tồn cộng dồn đến cuối kỳ." />
      <StockCardFilters
        sku={sku}
        skuId={skuId}
        today={today}
        value={{ from, to, color, unitId }}
        colors={COLORS.map((c) => c.key as ColorKey).filter((k) => colors.includes(k)).concat(colors.includes("none") ? ["none"] : [])}
        onSku={(id) => url.set({ sku: id, color: null, unit: null })}
        onChange={change}
      />
      {skuId === null ? (
        <p className="hint">Chọn mặt hàng để xem thẻ kho.</p>
      ) : card.isLoading ? (
        <ListLoading />
      ) : data ? (
        <>
          <StockCardKpis card={data} colorName={colorName} moves={moves} showCheck={to >= today} />
          <StockCardTable card={data} colorOf={colorDir.codeOf} showBase={unitId === null && multiUnit} />
          {data.rows.length === 0 ? (
            <p className="hint">
              Không có biến động trong khoảng {fd(from)} – {fd(to)}.
            </p>
          ) : null}
        </>
      ) : null}
      {skuId !== null ? <ListFooter list={card} /> : null}
    </>
  );
}
