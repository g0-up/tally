import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import { Combobox } from "../../../shared/ui/combobox";
import { DatePicker } from "../../../shared/ui/date-picker";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { COLORS, NO_COLOR_LABEL, type ColorKey } from "../../../shared/domain/color";
import { baseUnitName, useSkuPicker, type SkuListItem } from "../../catalog";
import { cardRanges, type CardRange, type ColorFilter } from "../view-model";

const RANGE_LABELS: ReadonlyArray<{ value: CardRange; label: string }> = [
  { value: "today", label: "Hôm nay" },
  { value: "7d", label: "7 ngày" },
  { value: "month", label: "Tháng này" },
  { value: "prev", label: "Tháng trước" },
  { value: "all", label: "Từ đầu" },
];

export interface StockCardFilterValue {
  from: string;
  to: string;
  color: ColorFilter;
  /** Fixed display unit, or null for "Quy đổi tự động". */
  unitId: string | null;
}

export interface StockCardFiltersProps {
  sku: SkuListItem | undefined;
  skuId: string | null;
  today: string;
  value: StockCardFilterValue;
  /** Colors the SKU has stock or moves in; chips show only when one is a real color. */
  colors: readonly ColorKey[];
  onSku: (skuId: string) => void;
  onChange: (patch: Partial<StockCardFilterValue>) => void;
}

/** Prototype `kho.the-kho` filter rows: product, period, range chips, color and unit chips. */
export function StockCardFilters({ sku, skuId, today, value, colors, onSku, onChange }: StockCardFiltersProps) {
  const picker = useSkuPicker(null);
  const ranges = cardRanges(today);
  const range = RANGE_LABELS.find((r) => ranges[r.value][0] === value.from && ranges[r.value][1] === value.to)?.value ?? "";
  const base = sku ? baseUnitName(sku) : "";
  const units = (sku?.units ?? []).filter((u) => u.active).sort((a, b) => dec(b.factor).cmp(a.factor));
  const colored = colors.some((c) => c !== "none");

  return (
    <>
      <FilterBar>
        <div style={{ minWidth: 320, flex: 1, maxWidth: 460 }}>
          <Combobox
            source={picker.source}
            filter={(it) => picker.picked(it.id)?.product_type !== "combo"}
            value={skuId}
            valueLabel={sku ? `${sku.code} · ${sku.display_name}` : ""}
            onChange={(it) => onSku(it.id)}
            aria-label="Tìm và chọn mặt hàng"
          />
        </div>
        <div className="f" style={{ gridAutoFlow: "column", alignItems: "center" }}>
          <span aria-hidden="true">Từ</span>
          <DatePicker value={value.from} onChange={(from) => from && onChange({ from })} aria-label="Từ ngày" />
        </div>
        <div className="f" style={{ gridAutoFlow: "column", alignItems: "center" }}>
          <span aria-hidden="true">Đến</span>
          <DatePicker value={value.to} onChange={(to) => to && onChange({ to })} aria-label="Đến ngày" />
        </div>
      </FilterBar>
      <FilterBar>
        <span className="hint">Khoảng ngày:</span>
        <ToggleGroup<CardRange | "">
          label="Khoảng ngày"
          options={RANGE_LABELS}
          value={range}
          onChange={(r) => r !== "" && onChange({ from: ranges[r][0], to: ranges[r][1] })}
        />
      </FilterBar>
      {colored ? (
        <FilterBar>
          <span className="hint">Màu:</span>
          <ToggleGroup<ColorFilter>
            label="Màu"
            options={[
              { value: "all", label: "Mọi màu" },
              ...COLORS.filter((c) => colors.includes(c.key)).map((c) => ({ value: c.key, label: c.label })),
              ...(colors.includes("none") ? [{ value: "none" as const, label: NO_COLOR_LABEL }] : []),
            ]}
            value={value.color}
            onChange={(color) => onChange({ color })}
          />
        </FilterBar>
      ) : null}
      {sku ? (
        <FilterBar>
          <span className="hint">Hiển thị số lượng:</span>
          <ToggleGroup
            label="Hiển thị số lượng"
            options={[
              { value: "auto", label: "Quy đổi tự động" },
              ...units.map((u) => ({ value: u.unit_id, label: dec(u.factor).eq(1) ? `Theo ${u.unit_name}` : `Theo ${u.unit_name} (${num(u.factor)} ${base})` })),
            ]}
            value={value.unitId ?? "auto"}
            onChange={(u) => onChange({ unitId: u === "auto" ? null : u })}
          />
        </FilterBar>
      ) : null}
    </>
  );
}
