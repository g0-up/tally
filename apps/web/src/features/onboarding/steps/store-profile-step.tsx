import { useState } from "react";
import type { Tenant } from "../../../shared/api/endpoints";
import { useBusinessDate, useTenant } from "../../../shared/api/platform-queries";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { ScreenError } from "../../../app/route-fallbacks";
import { useSaveTenantProfile, useStepCommand, type TenantProfilePatch } from "../api/mutations";
import { OnbField } from "../components/onb-field";
import { withoutError, type FieldErrors } from "../components/field-errors";
import { useOnbFail } from "../hooks/use-onb-fail";
import { useOnboardingScreen } from "../onboarding-context";
import { nextStep } from "../steps";

const NAME_MAX = 200;
const TAX_CODE = /^[0-9]{10}(-[0-9]{3})?$/;

interface Draft {
  legal: string;
  display: string;
  tax: string;
  phone: string;
  addr: string;
  cutoff: string | null;
}

/** The API's field names, for the errors of a refused save. */
const API_FIELDS: Readonly<Record<string, keyof Draft>> = {
  legal_name: "legal",
  display_name: "display",
  tax_code: "tax",
  phone: "phone",
  address: "addr",
  opening_cutoff_date: "cutoff",
};

const draftOf = (p: Tenant): Draft => ({
  legal: p.legal_name,
  display: p.display_name,
  tax: p.tax_code ?? "",
  phone: p.phone ?? "",
  addr: p.address ?? "",
  cutoff: p.opening_cutoff_date,
});

/** Prototype `A.onbSaveProfile` checks; the cutoff is not checked once it is frozen. */
function validate(d: Draft, today: string, confirmed: boolean): FieldErrors {
  const e: Record<string, string> = {};
  const legal = d.legal.trim();
  const display = d.display.trim();
  const tax = d.tax.trim();
  if (!legal) e.legal = "Nhập tên pháp lý.";
  else if (legal.length > NAME_MAX) e.legal = `Tối đa ${NAME_MAX} ký tự.`;
  if (!display) e.display = "Nhập tên hiển thị.";
  else if (display.length > NAME_MAX) e.display = `Tối đa ${NAME_MAX} ký tự.`;
  if (tax && !TAX_CODE.test(tax)) e.tax = "Mã số thuế gồm 10 số, hoặc 10 số-3 số (ví dụ 0312345678-001).";
  if (!confirmed) {
    if (d.cutoff === null) e.cutoff = "Chọn ngày chốt số dư đầu kỳ.";
    else if (d.cutoff > today) e.cutoff = `Ngày chốt không được sau hôm nay (${fd(today)}).`;
  }
  return e;
}

const orNull = (v: string) => (v.trim() === "" ? null : v.trim());

/** Store profile: names and contact printed on the documents, and the opening cutoff date. */
export function StoreProfileStep() {
  const profile = useTenant();
  const { goTo } = useOnboardingScreen();
  // Lives above the form, which starts again from the version the save returns.
  const complete = useStepCommand("store_profile", "complete", { quiet: true, after: () => goTo(nextStep("store_profile")) });
  if (profile.isPending) return <ListLoading />;
  if (profile.isError) return <ScreenError error={profile.error} onRetry={() => void profile.refetch()} />;
  return <ProfileForm key={profile.data.version} profile={profile.data} complete={complete} />;
}

function ProfileForm({ profile, complete }: { profile: Tenant; complete: ReturnType<typeof useStepCommand> }) {
  const { steps, confirmed, cta } = useOnboardingScreen();
  const today = useBusinessDate();
  const [draft, setDraft] = useState<Draft>(() => draftOf(profile));
  const [errors, setErrors] = useState<FieldErrors>({});
  const fail = useOnbFail(setErrors);
  const first = steps.find((s) => s.key === "store_profile")?.status !== "completed";
  const save = useSaveTenantProfile(profile, {
    setFieldError: (field, message) => {
      const key = API_FIELDS[field];
      if (key) setErrors((e) => ({ ...e, [key]: message }));
    },
    success: (saved) =>
      first ? (
        <>
          Đã lưu thông tin cửa hàng. Số dư đầu kỳ tính đến hết ngày <b>{fd(saved.opening_cutoff_date)}</b>.
        </>
      ) : (
        "Đã lưu thay đổi."
      ),
    after: () => {
      if (first) void complete.run();
    },
  });

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setErrors((e) => withoutError(e, key));
  };

  const submit = () => {
    const found = validate(draft, today, confirmed);
    if (Object.keys(found).length > 0) return fail(found);
    setErrors({});
    const patch: TenantProfilePatch = {
      legal_name: draft.legal.trim(),
      display_name: draft.display.trim(),
      tax_code: orNull(draft.tax),
      address: orNull(draft.addr),
      phone: orNull(draft.phone),
      ...(confirmed || draft.cutoff === null ? {} : { opening_cutoff_date: draft.cutoff }),
    };
    void save.run(patch);
  };

  return (
    <>
      <fieldset className="onb-fs">
        <legend>Pháp lý và liên hệ</legend>
        <div className="fg">
          <OnbField id="ob-pf-legal" label="Tên pháp lý" required hint="Như trên giấy đăng ký kinh doanh." error={errors.legal}>
            <Input id="ob-pf-legal" value={draft.legal} maxLength={NAME_MAX} autoComplete="organization" aria-required="true" onChange={(e) => set("legal", e.target.value)} />
          </OnbField>
          <OnbField id="ob-pf-display" label="Tên hiển thị" required hint="Hiện trên hóa đơn và màn chính." error={errors.display}>
            <Input id="ob-pf-display" value={draft.display} maxLength={NAME_MAX} aria-required="true" onChange={(e) => set("display", e.target.value)} />
          </OnbField>
          <OnbField id="ob-pf-tax" label="Mã số thuế" hint="10 số, hoặc 10 số-3 số với chi nhánh." error={errors.tax}>
            <Input id="ob-pf-tax" value={draft.tax} inputMode="numeric" autoComplete="off" onChange={(e) => set("tax", e.target.value)} />
          </OnbField>
          <OnbField id="ob-pf-phone" label="Số điện thoại" error={errors.phone}>
            <Input id="ob-pf-phone" type="tel" value={draft.phone} autoComplete="tel" onChange={(e) => set("phone", e.target.value)} />
          </OnbField>
          <OnbField id="ob-pf-addr" label="Địa chỉ" wide error={errors.addr}>
            <Input id="ob-pf-addr" value={draft.addr} autoComplete="street-address" onChange={(e) => set("addr", e.target.value)} />
          </OnbField>
        </div>
      </fieldset>
      <fieldset className="onb-fs">
        <legend>Ngày chốt số dư đầu kỳ</legend>
        <div className="onb-cut">
          <OnbField id="ob-pf-cutoff" label="Ngày chốt" required hint={confirmed ? "Đã khóa cùng số dư đầu kỳ." : `Không sau hôm nay (${fd(today)}).`} error={errors.cutoff}>
            <DatePicker id="ob-pf-cutoff" value={draft.cutoff} onChange={(v) => set("cutoff", v)} disabled={confirmed} required today={today} />
          </OnbField>
          <Callout role="note">
            Tồn kho, công nợ và tiền ở các bước sau là số dư <b>đến hết ngày này</b>. Thường chọn ngày cuối tháng trước khi bắt đầu dùng phần mềm. Sau khi xác nhận số dư, ngày chốt không sửa được.
          </Callout>
        </div>
      </fieldset>
      <ButtonRow>
        <Button variant={cta} pending={save.pending || complete.pending} onClick={submit}>
          {first ? "Lưu và tiếp tục" : "Lưu thay đổi"}
        </Button>
      </ButtonRow>
    </>
  );
}
