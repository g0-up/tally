import { useId, useState } from "react";
import { colorLabel } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { MoneyInput } from "../../../shared/ui/money-input";
import { toast } from "../../../shared/ui/toast-store";
import { baseUnitName, type SkuListItem } from "../../catalog";
import { marginText } from "../../pricing";
import { colorKeyOf, INVENTORY_MESSAGES } from "../api/adapters";
import { useSetColorPrice } from "../api/mutations";
import type { ColorPrice } from "../api/queries";
import type { ColorStock } from "../schemas";

export interface ColorPriceDialogProps {
  sku: SkuListItem;
  color: ColorStock;
  /** The color's own prices; undefined when it follows the product price. */
  own: ColorPrice | undefined;
  onClose: () => void;
}

interface Draft {
  retail: Money | null;
  wholesale: Money | null;
  /** "Bỏ giá riêng": go back to the product price. */
  base: boolean;
}

const same = (a: Money | null, b: Money | null) => (a === null || b === null ? a === b : dec(a).eq(b));

/**
 * Prototype `colorPrice`: one color's own retail and wholesale price. "Bỏ giá riêng" keeps the
 * inputs focusable but read-only and says which price applies instead. Mount per opening.
 */
export function ColorPriceDialog({ sku, color, own, onClose }: ColorPriceDialogProps) {
  const key = colorKeyOf(color.color_code);
  const name = colorLabel(key);
  const hasOwn = own !== undefined && (own.retail !== null || own.wholesale !== null);
  // An empty wholesale price follows the product's; only one typed here becomes the color's own.
  const [draft, setDraft] = useState<Draft>({ retail: own?.retail ?? sku.retail_price, wholesale: own?.wholesale ?? null, base: false });
  const save = useSetColorPrice(onClose);
  // While the color follows the product price the fields are read-only; the line saying so describes them.
  const baseHintId = useId();
  const describedBy = draft.base ? baseHintId : undefined;
  const base = baseUnitName(sku);
  const set = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }));

  const submit = () => {
    if (color.color_id === null || color.color_id === undefined) return;
    const common = {
      skuId: sku.id,
      colorId: color.color_id,
      colorName: name,
      shared: { retail: sku.retail_price, wholesale: sku.wholesale_price },
      own: { retail: own?.retail ?? null, wholesale: own?.wholesale ?? null },
    };
    if (draft.base) {
      if (!hasOwn) {
        toast.commitError(INVENTORY_MESSAGES.noColorPrice);
        return;
      }
      void save.run({ ...common, next: null });
      return;
    }
    const { retail, wholesale } = draft;
    if (retail === null || !dec(retail).gt(0)) {
      toast.commitError(INVENTORY_MESSAGES.retailPositive);
      return;
    }
    const effectiveWholesale = wholesale ?? sku.wholesale_price;
    if (effectiveWholesale !== null && dec(effectiveWholesale).gt(retail)) {
      toast.commitError(INVENTORY_MESSAGES.wholesaleAboveRetail);
      return;
    }
    if (hasOwn && same(retail, own.retail) && same(wholesale, own.wholesale)) {
      toast.commitError(INVENTORY_MESSAGES.unchanged);
      return;
    }
    void save.run({ ...common, next: { retail, wholesale } });
  };

  const common = `${vnd(sku.retail_price)} / sỉ ${vnd(sku.wholesale_price)}`;
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Giá bán ${name} · ${sku.display_name}`}
      sub={hasOwn ? "Màu này đang dùng giá riêng." : `Màu này đang dùng giá chung ${vnd(sku.retail_price)} lẻ · ${vnd(sku.wholesale_price)} sỉ. Lưu để đặt giá riêng.`}
      submitLabel="Lưu giá màu"
      onSubmit={submit}
      submitDisabled={color.color_id === null || color.color_id === undefined}
      pending={save.pending}
      live={
        <ImpactSummary
          items={[
            { label: "Giá chung sản phẩm", value: common },
            { label: "Giá vốn BQ", value: sku.avg_cost === null ? "—" : vnd(sku.avg_cost) },
            { label: "Biên giá lẻ màu này", value: draft.base ? "—" : marginText(draft.retail, sku.avg_cost) },
            { label: "Tồn màu này", value: `${num(color.on_hand)} ${base}` },
          ]}
        />
      }
    >
      <FormGrid>
        <Field label={`Giá bán lẻ / ${base}`}>
          <MoneyInput value={draft.base ? sku.retail_price : draft.retail} onChange={(retail) => set({ retail })} readOnly={draft.base} aria-describedby={describedBy} autoFocus />
        </Field>
        <Field label={`Giá sỉ / ${base}`}>
          <MoneyInput
            value={draft.base ? sku.wholesale_price : draft.wholesale}
            onChange={(wholesale) => set({ wholesale })}
            readOnly={draft.base}
            aria-describedby={describedBy}
            {...(sku.wholesale_price === null ? {} : { placeholder: `Theo giá chung ${num(sku.wholesale_price)}` })}
          />
        </Field>
      </FormGrid>
      {hasOwn ? <Checkbox label={`Bỏ giá riêng, dùng giá chung của sản phẩm (${vnd(sku.retail_price)})`} checked={draft.base} onChange={(e) => set({ base: e.target.checked })} /> : null}
      {draft.base ? (
        <p className="hint" id={baseHintId}>
          Giá chung {common} sẽ áp dụng cho màu này.
        </p>
      ) : null}
    </CommitDialog>
  );
}
