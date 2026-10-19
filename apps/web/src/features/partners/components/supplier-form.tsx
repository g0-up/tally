import { useId } from "react";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import type { SupplierDraft, SupplierErrors } from "../supplier-form-values";

export interface SupplierFormFieldsProps {
  draft: SupplierDraft;
  onChange: (patch: Partial<SupplierDraft>) => void;
  errors: SupplierErrors;
  /** supQuick: name, phone, contact and terms only. */
  quick?: boolean;
  /** The partner already buys from the shop; the customer role cannot be removed here. */
  customerLocked?: boolean;
}

/** Prototype supModal body (or the supQuick subset). */
export function SupplierFormFields({ draft, onChange, errors, quick = false, customerLocked = false }: SupplierFormFieldsProps) {
  const text = (field: "name" | "contact" | "phone" | "taxCode" | "address" | "goods" | "note", extra: { placeholder?: string; inputMode?: "tel" } = {}) => (
    <Input value={draft[field]} onChange={(e) => onChange({ [field]: e.target.value })} invalid={errors[field] !== undefined} {...extra} />
  );
  const lockHintId = useId();
  const terms = <Input inputMode="numeric" value={draft.terms} onChange={(e) => onChange({ terms: e.target.value })} invalid={errors.terms !== undefined} />;

  if (quick) {
    return (
      <FormGrid>
        <Field label="Tên nhà cung cấp *" error={errors.name}>
          {text("name")}
        </Field>
        <Field label="Số điện thoại" error={errors.phone}>
          {text("phone", { inputMode: "tel" })}
        </Field>
        <Field label="Người liên hệ" error={errors.contact}>
          {text("contact")}
        </Field>
        <Field label="Số ngày nợ" error={errors.terms}>
          {terms}
        </Field>
      </FormGrid>
    );
  }

  return (
    <>
      <FormGrid>
        <Field label="Tên nhà cung cấp *" error={errors.name}>
          {text("name", { placeholder: "VD: Công ty Bánh kẹo An Phú" })}
        </Field>
        <Field label="Người liên hệ" error={errors.contact}>
          {text("contact")}
        </Field>
        <Field label="Số điện thoại" error={errors.phone}>
          {text("phone", { inputMode: "tel" })}
        </Field>
        <Field label="Mã số thuế" error={errors.taxCode}>
          {text("taxCode")}
        </Field>
      </FormGrid>
      <Field label="Địa chỉ" error={errors.address}>
        {text("address")}
      </Field>
      <FormGrid>
        <Field label="Số ngày nợ được phép" hint="0 = trả ngay khi nhận hàng" error={errors.terms}>
          {terms}
        </Field>
        <Field label="Mặt hàng cung cấp" error={errors.goods}>
          {text("goods", { placeholder: "VD: bánh quy, kẹo, mứt" })}
        </Field>
      </FormGrid>
      <Field label="Ghi chú" error={errors.note}>
        {text("note")}
      </Field>
      <Checkbox
        checked={draft.alsoCustomer}
        disabled={customerLocked}
        aria-describedby={customerLocked ? lockHintId : undefined}
        onChange={(e) => onChange({ alsoCustomer: e.target.checked })}
        label="Đối tác này đồng thời mua hàng của cửa hàng (công nợ mua và bán vẫn tách riêng)"
      />
      {customerLocked ? (
        <p className="hint" id={lockHintId}>
          Đối tác đã là khách hàng của cửa hàng nên không bỏ vai trò khách ở đây được.
        </p>
      ) : null}
    </>
  );
}
