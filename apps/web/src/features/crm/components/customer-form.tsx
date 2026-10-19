import { Checkbox } from "../../../shared/ui/checkbox";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { CONSENT_CHANNEL_LABELS, priceSourceText, type CustomerDraft, type CustomerErrors } from "../customer-form-values";
import { consentChannelSchema, type CustomerGroup, type PriceList } from "../schemas";

const KIND_LABELS = { person: "Cá nhân", organization: "Doanh nghiệp" } as const;

export interface CustomerFormFieldsProps {
  draft: CustomerDraft;
  onChange: (patch: Partial<CustomerDraft>) => void;
  errors: CustomerErrors;
  groups: readonly CustomerGroup[];
  priceLists: readonly PriceList[];
  /** The edited customer, for its own price lists; null when adding. */
  customerId: string | null;
}

/** Body of "Thêm khách hàng" / "Sửa khách hàng". */
export function CustomerFormFields({ draft, onChange, errors, groups, priceLists, customerId }: CustomerFormFieldsProps) {
  const text = (field: "name" | "phone" | "email" | "taxCode" | "address" | "note" | "consentSource", extra: { placeholder?: string; inputMode?: "tel" | "email" } = {}) => (
    <Input value={draft[field]} onChange={(e) => onChange({ [field]: e.target.value })} invalid={errors[field] !== undefined} {...extra} />
  );
  const group = groups.find((g) => g.id === draft.groupId);

  return (
    <>
      <FormGrid>
        <Field label="Tên khách hàng *" error={errors.name}>
          {text("name")}
        </Field>
        <Field label="Loại khách">
          <select className="in" value={draft.kind} onChange={(e) => onChange({ kind: e.target.value === "organization" ? "organization" : "person" })}>
            {(["person", "organization"] as const).map((k) => (
              <option key={k} value={k}>
                {KIND_LABELS[k]}
              </option>
            ))}
          </select>
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
        <Field label="Email" error={errors.email}>
          {text("email", { inputMode: "email" })}
        </Field>
        <Field label="Nhóm khách" error={errors.groupId} hint={`Bảng giá áp dụng: ${priceSourceText(group, priceLists, customerId)}`}>
          <select className="in" value={draft.groupId} onChange={(e) => onChange({ groupId: e.target.value })} aria-invalid={errors.groupId ? true : undefined}>
            {draft.groupId === "" ? <option value="">Mặc định (nhóm bán lẻ)</option> : null}
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Hạn mức nợ" hint="Để trống = không giới hạn" error={errors.creditLimit}>
          <MoneyInput value={draft.creditLimit} onChange={(creditLimit) => onChange({ creditLimit })} invalid={errors.creditLimit !== undefined} />
        </Field>
        <Field label="Số ngày nợ được phép" hint="0 = trả ngay khi nhận hàng" error={errors.terms}>
          <Input inputMode="numeric" value={draft.terms} onChange={(e) => onChange({ terms: e.target.value })} invalid={errors.terms !== undefined} />
        </Field>
      </FormGrid>
      <Field label="Ghi chú" error={errors.note}>
        {text("note")}
      </Field>
      <Checkbox checked={draft.consent} onChange={(e) => onChange({ consent: e.target.checked })} label="Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ" />
      <FormGrid>
        <Field label="Kênh nhận tin">
          <select
            className="in"
            value={draft.consentChannel}
            onChange={(e) => {
              const channel = consentChannelSchema.safeParse(e.target.value);
              if (channel.success) onChange({ consentChannel: channel.data });
            }}
          >
            {consentChannelSchema.options.map((c) => (
              <option key={c} value={c}>
                {CONSENT_CHANNEL_LABELS[c]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Nguồn đồng ý" hint="Bắt buộc khi đổi đồng ý nhận tin" error={errors.consentSource}>
          {text("consentSource", { placeholder: "VD: tại quầy, qua điện thoại" })}
        </Field>
      </FormGrid>
      <p className="hint">Chưa có đồng ý thì tin khuyến mãi và chiến dịch bỏ qua khách này.</p>
    </>
  );
}
