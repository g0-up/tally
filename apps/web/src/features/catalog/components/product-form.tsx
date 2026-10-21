import { useMemo } from "react";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Combobox } from "../../../shared/ui/combobox";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { QtyInput } from "../../../shared/ui/qty-input";
import { useCategories, useUnits } from "../api/queries";
import { AttributeFields } from "../attribute-field-registry";
import type { AttributeValue, SetAttribute } from "../attribute-values";
import type { ProductDraft, ProductErrors, ProductFormVariant } from "./product-draft";
import { UnitPriceTable } from "./unit-price-table";

const LABELS = {
  catalog: { cost: "Giá mua tham khảo", track: "Quản lý theo lô và hạn dùng" },
  quick: { cost: "Giá nhập", track: "Quản lý lô và hạn dùng" },
} as const;

export interface ProductFormFieldsProps {
  variant: ProductFormVariant;
  draft: ProductDraft;
  onChange: (patch: Partial<ProductDraft>) => void;
  errors: ProductErrors;
  /** Attributes of the chosen category's set (catalog variant). */
  attributes: readonly SetAttribute[];
}

/** Prototype newSKU / newQuickProd fields (`.fg` grid, the unit table, then the lot checkbox and attributes). */
export function ProductFormFields({ variant, draft, onChange, errors, attributes }: ProductFormFieldsProps) {
  const units = useUnits();
  const categories = useCategories();
  const categoryItems = useMemo(() => (categories.data ?? []).map((c) => ({ id: c.id, name: c.name })), [categories.data]);
  const l = LABELS[variant];
  const base = units.name(draft.baseUnitId) || "ĐV cơ sở";
  const attributeErrors = useMemo(() => {
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(errors)) if (k.startsWith("attributes.")) out[k.slice("attributes.".length)] = v;
    return out;
  }, [errors]);

  const category = (
    <Field label="Nhóm" error={errors.category_id}>
      <Combobox items={categoryItems} value={draft.categoryId} onChange={(it) => onChange({ categoryId: it.id, attributes: {} })} aria-label="Nhóm" placeholder="Chọn nhóm…" invalid={errors.category_id !== undefined} />
    </Field>
  );
  const cost = (
    <Field label={`${l.cost} / ${base}`} error={errors.ref_cost ?? errors.purchase_price}>
      <MoneyInput value={draft.cost} onChange={(c) => onChange({ cost: c })} invalid={(errors.ref_cost ?? errors.purchase_price) !== undefined} />
    </Field>
  );

  return (
    <>
      {variant === "catalog" ? (
        <FormGrid>
          <Field label="Mã SKU" error={errors.code}>
            <Input value={draft.code} onChange={(e) => onChange({ code: e.target.value })} invalid={errors.code !== undefined} />
          </Field>
          <Field label="Tên hàng" error={errors.name}>
            <Input value={draft.name} onChange={(e) => onChange({ name: e.target.value })} invalid={errors.name !== undefined} />
          </Field>
          {category}
          <Field label="Mã vạch" error={errors.barcode}>
            <Input value={draft.barcode} onChange={(e) => onChange({ barcode: e.target.value })} invalid={errors.barcode !== undefined} />
          </Field>
          {cost}
          <Field label={`Tồn tối thiểu (${base})`} error={errors.min_stock}>
            <QtyInput value={draft.minStock} onChange={(minStock) => onChange({ minStock })} invalid={errors.min_stock !== undefined} />
          </Field>
        </FormGrid>
      ) : (
        <FormGrid>
          <Field label="Tên hàng hóa" error={errors.name}>
            <Input value={draft.name} onChange={(e) => onChange({ name: e.target.value })} invalid={errors.name !== undefined} />
          </Field>
          {category}
          {cost}
        </FormGrid>
      )}
      <UnitPriceTable value={draft} onChange={onChange} errors={errors} />
      <Checkbox label={l.track} checked={draft.trackLot} onChange={(e) => onChange({ trackLot: e.target.checked })} />
      {variant === "catalog" ? (
        <AttributeFields
          items={attributes}
          values={draft.attributes}
          errors={attributeErrors}
          onChange={(code: string, value: AttributeValue) => onChange({ attributes: { ...draft.attributes, [code]: value } })}
        />
      ) : null}
    </>
  );
}
