import { useState } from "react";
import { dec, isDecimalString } from "../../../shared/lib/decimal";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { DecimalInput } from "../../../shared/ui/money-input";
import { useCreatePartner } from "../api/mutations";

/** Same pattern as the API's referral code, after upper-casing. */
const REFERRAL_CODE = /^[A-Z0-9][A-Z0-9-]{2,31}$/;
const MAX_NAME = 200;

/** A percent typed the Vietnamese way ("12,5"); null when not a number. */
function parsePercentInput(text: string): string | null {
  const cleaned = text.replace(/[\s\u00a0%]/g, "").replace(/\./g, "").replace(",", ".");
  return isDecimalString(cleaned) ? cleaned : null;
}

type Errors = { name?: string | undefined; code?: string | undefined; percent?: string | undefined };

export function CreatePartnerDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [percent, setPercent] = useState<string | null>(null);
  const [errors, setErrors] = useState<Errors>({});
  const create = useCreatePartner({
    success: (p) => (
      <>
        Đã thêm đối tác <b>{p.name}</b>.
      </>
    ),
    after: onClose,
    setFieldError: (field, message) => setErrors((e) => ({ ...e, [field === "referral_code" ? "code" : field === "commission_percent" ? "percent" : "name"]: message })),
  });
  const submit = () => {
    const n = name.trim();
    const c = code.trim().toUpperCase();
    const next: Errors = {
      name: n === "" || n.length > MAX_NAME ? `Nhập tên đối tác, tối đa ${MAX_NAME} ký tự.` : undefined,
      code: REFERRAL_CODE.test(c) ? undefined : "Mã giới thiệu gồm 3 đến 32 chữ cái, chữ số hoặc dấu gạch ngang, không bắt đầu bằng dấu gạch ngang.",
      percent: percent !== null && !dec(percent).isNegative() && dec(percent).lte(100) ? undefined : "Nhập tỷ lệ hoa hồng từ 0 đến 100.",
    };
    setErrors(next);
    if (next.name || next.code || next.percent || percent === null) return;
    void create.run({ name: n, referral_code: c, commission_percent: percent });
  };
  return (
    <CommitDialog open onClose={onClose} title="Thêm đối tác giới thiệu" sub="Hoa hồng tính trên hóa đơn thuê bao đã thanh toán của cửa hàng được giới thiệu." onSubmit={submit} submitLabel="Thêm đối tác" pending={create.pending}>
      <Field label="Tên đối tác" error={errors.name}>
        <Input value={name} invalid={errors.name !== undefined} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Mã giới thiệu" error={errors.code} hint="Lưu bằng chữ in hoa; cửa hàng nhập không phân biệt hoa thường.">
        <Input value={code} invalid={errors.code !== undefined} autoComplete="off" spellCheck={false} onChange={(e) => setCode(e.target.value.toUpperCase())} />
      </Field>
      <Field label="Hoa hồng (%)" error={errors.percent}>
        <DecimalInput value={percent} onChange={setPercent} parse={parsePercentInput} invalid={errors.percent !== undefined} />
      </Field>
    </CommitDialog>
  );
}
