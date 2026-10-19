import { useState } from "react";
import type { Tenant } from "../../../shared/api/endpoints";
import { useBusinessDate, useTenant } from "../../../shared/api/platform-queries";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { ScreenError } from "../../../app/route-fallbacks";
import { useSaveTenantProfile, useStepCommand, type TenantProfilePatch } from "../api/mutations";
import { StepCard } from "../components/step-card";
import type { ChecklistStep } from "../steps";

const PROFILE_LABELS = {
  legalName: "Tên pháp lý",
  displayName: "Tên hiển thị",
  taxCode: "Mã số thuế",
  address: "Địa chỉ",
  phone: "Số điện thoại",
  cutoff: "Ngày chốt số dư đầu kỳ",
  saveAndDone: "Lưu và đánh dấu xong",
  save: "Lưu thay đổi",
} as const;

const NAME_MAX = 200;
const TAX_CODE = /^[0-9]{10}(-[0-9]{3})?$/;

interface Draft {
  legal_name: string;
  display_name: string;
  tax_code: string;
  address: string;
  phone: string;
  opening_cutoff_date: string | null;
}

type Errors = Partial<Record<keyof Draft, string>>;

const draftOf = (p: Tenant): Draft => ({
  legal_name: p.legal_name,
  display_name: p.display_name,
  tax_code: p.tax_code ?? "",
  address: p.address ?? "",
  phone: p.phone ?? "",
  opening_cutoff_date: p.opening_cutoff_date,
});

function validate(d: Draft, today: string): Errors {
  const e: Errors = {};
  if (d.legal_name.trim() === "") e.legal_name = "Nhập tên pháp lý.";
  else if (d.legal_name.trim().length > NAME_MAX) e.legal_name = `Tên pháp lý tối đa ${NAME_MAX} ký tự.`;
  if (d.display_name.trim() === "") e.display_name = "Nhập tên hiển thị.";
  else if (d.display_name.trim().length > NAME_MAX) e.display_name = `Tên hiển thị tối đa ${NAME_MAX} ký tự.`;
  if (d.tax_code.trim() !== "" && !TAX_CODE.test(d.tax_code.trim())) e.tax_code = "Mã số thuế gồm 10 chữ số, hoặc 10 chữ số kèm -3 chữ số chi nhánh.";
  if (d.opening_cutoff_date === null) e.opening_cutoff_date = "Chọn ngày chốt số dư đầu kỳ.";
  else if (d.opening_cutoff_date > today) e.opening_cutoff_date = "Ngày chốt số dư đầu kỳ không được sau hôm nay.";
  return e;
}

const orNull = (v: string) => (v.trim() === "" ? null : v.trim());

/** Store profile: legal and display names, tax code, contact and the opening cutoff date. */
export function StoreProfileStep({ step, confirmed }: { step: ChecklistStep; confirmed: boolean }) {
  const profile = useTenant();
  // Lives above the form, which remounts on the new version the save returns.
  const complete = useStepCommand("store_profile", "complete", { quiet: true });
  const onSaved = () => {
    if (step.status === "pending") void complete.run();
  };
  return (
    <StepCard step={step} note="Thông tin in trên chứng từ và ngày chốt số dư đầu kỳ" markDone={false}>
      {profile.isPending ? (
        <ListLoading />
      ) : profile.isError ? (
        <ScreenError error={profile.error} onRetry={() => void profile.refetch()} />
      ) : (
        <ProfileForm key={profile.data.version} profile={profile.data} step={step} confirmed={confirmed} onSaved={onSaved} completing={complete.pending} />
      )}
    </StepCard>
  );
}

interface ProfileFormProps {
  profile: Tenant;
  step: ChecklistStep;
  confirmed: boolean;
  onSaved: () => void;
  completing: boolean;
}

function ProfileForm({ profile, step, confirmed, onSaved, completing }: ProfileFormProps) {
  const [draft, setDraft] = useState<Draft>(() => draftOf(profile));
  const [errors, setErrors] = useState<Errors>({});
  const today = useBusinessDate();
  const save = useSaveTenantProfile(profile, {
    setFieldError: (field, message) => setErrors((e) => ({ ...e, [field]: message })),
    after: onSaved,
  });

  const set = (patch: Partial<Draft>) => {
    setDraft((d) => ({ ...d, ...patch }));
    setErrors((e) => Object.fromEntries(Object.entries(e).filter(([k]) => !(k in patch))));
  };

  const submit = () => {
    const found = validate(draft, today);
    if (confirmed) delete found.opening_cutoff_date;
    setErrors(found);
    const first = Object.values(found)[0];
    if (first !== undefined) return toast.commitError(first);
    const patch: TenantProfilePatch = {
      legal_name: draft.legal_name.trim(),
      display_name: draft.display_name.trim(),
      tax_code: orNull(draft.tax_code),
      address: orNull(draft.address),
      phone: orNull(draft.phone),
      ...(confirmed || draft.opening_cutoff_date === null ? {} : { opening_cutoff_date: draft.opening_cutoff_date }),
    };
    void save.run(patch);
  };

  return (
    <>
      <FormGrid>
        <Field label={PROFILE_LABELS.legalName} error={errors.legal_name}>
          <Input value={draft.legal_name} maxLength={NAME_MAX} invalid={errors.legal_name !== undefined} onChange={(e) => set({ legal_name: e.target.value })} />
        </Field>
        <Field label={PROFILE_LABELS.displayName} error={errors.display_name}>
          <Input value={draft.display_name} maxLength={NAME_MAX} invalid={errors.display_name !== undefined} onChange={(e) => set({ display_name: e.target.value })} />
        </Field>
        <Field label={PROFILE_LABELS.taxCode} hint="10 chữ số, chi nhánh thêm -3 chữ số" error={errors.tax_code}>
          <Input value={draft.tax_code} inputMode="numeric" invalid={errors.tax_code !== undefined} onChange={(e) => set({ tax_code: e.target.value })} />
        </Field>
        <Field label={PROFILE_LABELS.phone} error={errors.phone}>
          <Input value={draft.phone} inputMode="tel" invalid={errors.phone !== undefined} onChange={(e) => set({ phone: e.target.value })} />
        </Field>
        <Field label={PROFILE_LABELS.address} error={errors.address}>
          <Input value={draft.address} invalid={errors.address !== undefined} onChange={(e) => set({ address: e.target.value })} />
        </Field>
        <Field
          label={PROFILE_LABELS.cutoff}
          hint={confirmed ? "Đã khóa vì số dư đầu kỳ đã xác nhận." : "Tồn kho, công nợ và tiền đầu kỳ tính đến hết ngày này."}
          error={errors.opening_cutoff_date}
        >
          <DatePicker
            value={draft.opening_cutoff_date}
            onChange={(v) => set({ opening_cutoff_date: v })}
            disabled={confirmed}
            invalid={errors.opening_cutoff_date !== undefined}
            today={today}
            aria-label={PROFILE_LABELS.cutoff}
          />
        </Field>
      </FormGrid>
      <ButtonRow>
        <Button variant="primary" pending={save.pending || completing} onClick={submit}>
          {step.status === "pending" ? PROFILE_LABELS.saveAndDone : PROFILE_LABELS.save}
        </Button>
      </ButtonRow>
    </>
  );
}
