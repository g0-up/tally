import { Fragment, useState } from "react";
import { num, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { MoneyInput } from "../../../shared/ui/money-input";
import { baseUnitName, type SkuListItem } from "../../catalog";
import { useSaveSalePrice } from "../api/mutations";
import { usePriceListItems, usePriceLists } from "../api/queries";
import {
  PRICE_MESSAGES,
  colorPricesOf,
  marginText,
  priceableUnits,
  unitAutoPrices,
  unitPriceDrafts,
  wholesaleAboveRetail,
  wholesaleBelowMin,
  type PriceDraft,
  type UnitPriceDraft,
} from "../price-edit";
import type { PriceChangeSource } from "../schemas";
import { PriceHistoryTable } from "./price-history-table";

export interface EditPriceDialogProps {
  sku: SkuListItem;
  /** Screen the edit is made from; recorded as "Nơi sửa" in the price history. */
  source: PriceChangeSource;
  onClose: () => void;
}

/** Colors of the SKU that carry their own retail or wholesale base price. */
function useColorPrices(skuId: string) {
  const lists = usePriceLists();
  const listId = (kind: "retail" | "wholesale") => lists.data?.find((l) => l.kind === kind)?.id ?? null;
  const retail = usePriceListItems(listId("retail"), skuId);
  const wholesale = usePriceListItems(listId("wholesale"), skuId);
  return colorPricesOf([...(retail.data ?? []), ...(wholesale.data ?? [])]);
}

const NO_OWN_PRICE: UnitPriceDraft = { retail: null, wholesale: null };

/**
 * Prototype `editPrice` (Nhập hàng → Danh sách hàng hóa, Kho → Tồn kho): the general retail and
 * wholesale price of a SKU, optionally dropping every color price, and the own prices of its other
 * active units; a blank unit price sells that unit at the base price × factor. Mount per opening.
 */
export function EditPriceDialog({ sku, source, onClose }: EditPriceDialogProps) {
  const [draft, setDraft] = useState<PriceDraft>(() => ({ retail: sku.retail_price, wholesale: sku.wholesale_price, allColors: false, units: unitPriceDrafts(sku) }));
  const colorPrices = useColorPrices(sku.id);
  const { save, pending } = useSaveSalePrice({ sku, source, after: onClose });
  const base = baseUnitName(sku);
  const n = colorPrices.length;
  const set = (patch: Partial<PriceDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const setUnit = (unitId: string, patch: Partial<UnitPriceDraft>) =>
    setDraft((d) => ({ ...d, units: { ...d.units, [unitId]: { ...(d.units?.[unitId] ?? NO_OWN_PRICE), ...patch } } }));
  const units = priceableUnits(sku);

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Sửa giá bán · ${sku.display_name}`}
      sub={`Giá chung của sản phẩm. Áp dụng cho giao dịch bán mới; hóa đơn và báo giá đã lập giữ giá đã chụp.${n ? ` Đang có ${n} màu dùng giá riêng.` : ""}`}
      submitLabel="Lưu giá"
      onSubmit={() => save(draft)}
      pending={pending}
      live={
        <>
          <ImpactSummary
            items={[
              { label: "Giá vốn BQ", value: sku.avg_cost === null ? "—" : vnd(sku.avg_cost) },
              { label: "Biên giá lẻ", value: marginText(draft.retail, sku.avg_cost) },
              { label: "Biên giá sỉ", value: marginText(draft.wholesale ?? sku.wholesale_price, sku.avg_cost) },
              { label: "Giá tối thiểu (cảnh báo nếu bán thấp hơn)", value: sku.min_price === null ? "—" : vnd(sku.min_price) },
            ]}
          />
          {wholesaleAboveRetail(draft, sku) ? <Callout tone="warn">{PRICE_MESSAGES.wholesaleAboveRetail}</Callout> : null}
          {wholesaleBelowMin(draft, sku, sku.min_price) ? <Callout tone="warn">{PRICE_MESSAGES.belowMin}</Callout> : null}
          <PriceHistoryTable skuId={sku.id} />
        </>
      }
    >
      <FormGrid>
        <Field label={`Giá bán lẻ / ${base}`}>
          <MoneyInput value={draft.retail} onChange={(retail) => set({ retail })} autoFocus />
        </Field>
        <Field label={`Giá sỉ / ${base}`}>
          <MoneyInput value={draft.wholesale} onChange={(wholesale) => set({ wholesale })} />
        </Field>
      </FormGrid>
      {units.length > 0 ? (
        <>
          <p className="hint">Giá theo đơn vị quy đổi: bỏ trống (hoặc xóa giá đang có) để tự tính theo giá / {base} × hệ số.</p>
          <FormGrid>
            {units.map((u) => {
              const own = draft.units?.[u.unit_id] ?? NO_OWN_PRICE;
              const auto = unitAutoPrices(draft, sku, u);
              const factor = `1 ${u.unit_name} = ${num(u.factor)} ${base}`;
              return (
                <Fragment key={u.unit_id}>
                  <Field label={`Giá bán lẻ / ${u.unit_name}`} hint={factor}>
                    <MoneyInput value={own.retail} onChange={(retail) => setUnit(u.unit_id, { retail })} placeholder={`${num(auto.retail)} tự tính`} />
                  </Field>
                  <Field label={`Giá sỉ / ${u.unit_name}`}>
                    <MoneyInput value={own.wholesale} onChange={(wholesale) => setUnit(u.unit_id, { wholesale })} placeholder={`${num(auto.wholesale)} tự tính`} />
                  </Field>
                </Fragment>
              );
            })}
          </FormGrid>
        </>
      ) : null}
      {n > 0 ? (
        <Checkbox
          label={`Áp dụng cho tất cả màu (bỏ giá riêng của ${n} màu: ${colorPrices.map((c) => c.name).join(", ")})`}
          checked={draft.allColors}
          onChange={(e) => set({ allColors: e.target.checked })}
        />
      ) : null}
    </CommitDialog>
  );
}
