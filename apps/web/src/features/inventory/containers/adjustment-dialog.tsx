import { useState } from "react";
import { colorLabel, type ColorKey } from "../../../shared/domain/color";
import type { Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { StockColorPicker } from "../../../shared/ui/color/stock-color-picker";
import { Combobox } from "../../../shared/ui/combobox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { QtyInput } from "../../../shared/ui/qty-input";
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitName, useSkuPicker, type SkuListItem } from "../../catalog";
import { ADJUSTMENT_KIND_LABELS, colorKeyOf, DECREASE_KINDS, INVENTORY_MESSAGES, type DecreaseKind } from "../api/adapters";
import { useCreateAdjustment, type AdjustmentLineRequest } from "../api/mutations";
import { useReasons, useStockDetail } from "../api/queries";
import { ReasonField } from "../components/reason-field";
import { EMPTY_REASON, reasonGiven, stockColors, type ReasonValue } from "../view-model";

export interface AdjustmentDialogProps {
  onClose: () => void;
}

interface Draft {
  kind: DecreaseKind;
  sku: SkuListItem | null;
  color: ColorKey | null;
  qty: Qty | null;
  reason: ReasonValue;
}

/**
 * Prototype `newADJ` "Điều chỉnh giảm kho": damage, loss, gift or internal use of one SKU (one
 * color when the stock has colors), with a mandatory reason. Goods leaving this way earn nothing.
 */
export function AdjustmentDialog({ onClose }: AdjustmentDialogProps) {
  const picker = useSkuPicker(null);
  const [draft, setDraft] = useState<Draft>({ kind: "damage", sku: null, color: null, qty: null, reason: EMPTY_REASON });
  const detail = useStockDetail(draft.sku?.id ?? null);
  const stock = detail.data?.sku_id === draft.sku?.id ? detail.data : undefined;
  const reasons = (useReasons().data ?? []).filter((r) => r.kind === draft.kind);
  const create = useCreateAdjustment(() => "Đã ghi điều chỉnh.", onClose);
  const set = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }));

  const colors = stock ? stockColors(stock) : [];
  const colored = colors.some((c) => colorKeyOf(c.color_code) !== "none");
  const picked = colored && draft.color !== null ? colors.find((c) => colorKeyOf(c.color_code) === draft.color) : undefined;
  const sellable = picked ? picked.sellable : stock?.sellable;
  const base = draft.sku ? baseUnitName(draft.sku) : "";
  const value = draft.qty !== null && stock ? dec(draft.qty).times(stock.avg_cost) : null;

  const submit = () => {
    const { sku, qty } = draft;
    if (!sku) return toast.commitError(INVENTORY_MESSAGES.productRequired);
    if (colored && !picked) return toast.commitError(INVENTORY_MESSAGES.colorRequired);
    if (qty === null || !dec(qty).gt(0)) return toast.commitError(INVENTORY_MESSAGES.qtyPositive);
    if (sellable !== undefined && dec(qty).gt(sellable)) return toast.commitError(INVENTORY_MESSAGES.overSellable(sellable, base));
    if (!reasonGiven(draft.reason, reasons)) return toast.commitError(INVENTORY_MESSAGES.reasonRequired);
    const line: AdjustmentLineRequest = { sku_id: sku.id, qty };
    if (picked?.color_id) line.color_id = picked.color_id;
    else if (picked) line.uncolored = true;
    void create.run({
      kind: draft.kind,
      reasonId: reasons.length > 0 ? draft.reason.reasonId : null,
      ...(reasons.length > 0 ? {} : { note: draft.reason.text.trim() }),
      lines: [line],
      name: sku.display_name,
    });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Điều chỉnh giảm kho"
      submitLabel="Ghi sổ điều chỉnh"
      onSubmit={submit}
      pending={create.pending}
      live={
        <ImpactSummary
          items={[
            { label: picked ? `Tồn bán được ${colorLabel(colorKeyOf(picked.color_code))}` : "Tồn bán được hiện tại", value: sellable === undefined ? "—" : num(sellable) },
            { label: "Giá trị ghi giảm", value: value === null ? "—" : vnd(value) },
            { label: "Doanh thu", value: "Không phát sinh" },
          ]}
        />
      }
    >
      <FormGrid>
        <Field label="Loại">
          <select className="in" value={draft.kind} onChange={(e) => set({ kind: e.target.value as DecreaseKind, reason: EMPTY_REASON })}>
            {DECREASE_KINDS.map((k) => (
              <option key={k} value={k}>
                {ADJUSTMENT_KIND_LABELS[k]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Hàng">
          <Combobox
            source={picker.source}
            filter={(it) => picker.picked(it.id)?.product_type !== "combo"}
            value={draft.sku?.id ?? null}
            valueLabel={draft.sku ? `${draft.sku.code} · ${draft.sku.display_name}` : ""}
            onChange={(it) => {
              const sku = picker.picked(it.id);
              if (sku) set({ sku, color: null });
            }}
            aria-label="Hàng"
          />
        </Field>
        <Field label={`Số lượng${base ? ` (${base})` : ""}`}>
          <QtyInput value={draft.qty} onChange={(qty) => set({ qty })} />
        </Field>
      </FormGrid>
      {colored ? (
        <StockColorPicker
          options={colors.map((c) => ({ color: colorKeyOf(c.color_code), qty: c.sellable }))}
          value={draft.color}
          onChange={(color) => set({ color })}
          base={base}
          label="Màu xuất"
        />
      ) : null}
      <ReasonField label="Lý do (bắt buộc)" reasons={reasons} value={draft.reason} onChange={(reason) => set({ reason })} />
    </CommitDialog>
  );
}
