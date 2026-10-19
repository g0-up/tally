import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { tenantSchema, type Tenant } from "../../../shared/api/endpoints";
import { useBusinessDate, useTenant } from "../../../shared/api/platform-queries";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { TAX_CODE_PATTERN, type StoreProfilePatch } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { copyText } from "./copy-text";
import { LoadError } from "./load-error";
import { useReadOnlyHint } from "./read-only-hint";

export const STORE_PROFILE_SAVED = "Đã lưu thông tin cửa hàng.";
export const STORE_URL_COPIED = "Đã sao chép địa chỉ cửa hàng.";
const NAME_REQUIRED = "Nhập tên cửa hàng.";
const LEGAL_NAME_REQUIRED = "Nhập tên pháp lý.";
const TAX_CODE_ERROR = "Mã số thuế gồm 10 chữ số, có thể thêm -XXX.";

type ProfileField = "display_name" | "legal_name" | "tax_code" | "address" | "phone" | "opening_cutoff_date";
const FIELDS: readonly ProfileField[] = ["display_name", "legal_name", "tax_code", "address", "phone", "opening_cutoff_date"];

interface Draft {
  display_name: string;
  legal_name: string;
  tax_code: string;
  address: string;
  phone: string;
  opening_cutoff_date: string | null;
}

function draftOf(p: Tenant): Draft {
  return {
    display_name: p.display_name,
    legal_name: p.legal_name,
    tax_code: p.tax_code ?? "",
    address: p.address ?? "",
    phone: p.phone ?? "",
    opening_cutoff_date: p.opening_cutoff_date,
  };
}

/** Only the fields the owner changed; cleared optional text goes as null, a cleared date as "". */
function changes(saved: Draft, draft: Draft): StoreProfilePatch {
  const patch: StoreProfilePatch = {};
  if (draft.display_name.trim() !== saved.display_name) patch.display_name = draft.display_name.trim();
  if (draft.legal_name.trim() !== saved.legal_name) patch.legal_name = draft.legal_name.trim();
  for (const f of ["tax_code", "address", "phone"] as const) {
    const v = draft[f].trim();
    if (v !== saved[f]) patch[f] = v === "" ? null : v;
  }
  if (draft.opening_cutoff_date !== saved.opening_cutoff_date) patch.opening_cutoff_date = draft.opening_cutoff_date ?? "";
  return patch;
}

function StoreUrl() {
  const url = window.location.origin;
  const copy = () => copyText(url, STORE_URL_COPIED, "Hãy chọn và sao chép địa chỉ.");
  return (
    <Field label="Địa chỉ cửa hàng" hint="Địa chỉ đăng nhập của cửa hàng; không đổi được.">
      <div style={{ display: "flex", gap: 8 }}>
        <Input readOnly value={url} />
        <Button size="sm" onClick={copy}>
          Sao chép
        </Button>
      </div>
    </Field>
  );
}

function ProfileForm({ profile }: { profile: Tenant }) {
  const client = useApiClient();
  const readOnly = useReadOnlyHint();
  const today = useBusinessDate();
  const saved = draftOf(profile);
  const [draft, setDraft] = useState<Draft>(saved);
  const [errors, setErrors] = useState<Partial<Record<ProfileField, string>>>({});

  const commit = useCommit({
    mutationFn: (patch: StoreProfilePatch) => client.patch("/tenant", patch, { ifMatch: `"${profile.version}"`, schema: tenantSchema }),
    success: () => STORE_PROFILE_SAVED,
    invalidate: [settingsKeys.owner(), platformKeys.tenant(), platformKeys.me(), platformKeys.onboarding()],
    setFieldError: (field, message) => {
      if ((FIELDS as readonly string[]).includes(field)) setErrors((cur) => ({ ...cur, [field]: message }));
    },
  });

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((cur) => ({ ...cur, [key]: value }));

  const dirty = Object.keys(changes(saved, draft)).length > 0;

  const save = () => {
    const next: Partial<Record<ProfileField, string>> = {};
    if (draft.display_name.trim() === "") next.display_name = NAME_REQUIRED;
    if (draft.legal_name.trim() === "") next.legal_name = LEGAL_NAME_REQUIRED;
    const tax = draft.tax_code.trim();
    if (tax !== "" && !TAX_CODE_PATTERN.test(tax)) next.tax_code = TAX_CODE_ERROR;
    setErrors(next);
    if (Object.keys(next).length > 0) return;
    void commit.run(changes(saved, draft));
  };

  return (
    <>
      <FormGrid>
        <Field label="Tên cửa hàng" error={errors.display_name}>
          <Input value={draft.display_name} maxLength={200} onChange={(e) => set("display_name", e.target.value)} invalid={errors.display_name !== undefined} />
        </Field>
        <Field label="Tên pháp lý" hint="Tên trên giấy đăng ký kinh doanh, in trên chứng từ." error={errors.legal_name}>
          <Input value={draft.legal_name} maxLength={200} onChange={(e) => set("legal_name", e.target.value)} invalid={errors.legal_name !== undefined} />
        </Field>
        <Field label="Mã số thuế" error={errors.tax_code}>
          <Input inputMode="numeric" value={draft.tax_code} maxLength={14} onChange={(e) => set("tax_code", e.target.value)} invalid={errors.tax_code !== undefined} />
        </Field>
        <Field label="Điện thoại" error={errors.phone}>
          <Input inputMode="tel" value={draft.phone} maxLength={20} onChange={(e) => set("phone", e.target.value)} invalid={errors.phone !== undefined} />
        </Field>
        <Field label="Địa chỉ" error={errors.address}>
          <Input value={draft.address} maxLength={500} onChange={(e) => set("address", e.target.value)} invalid={errors.address !== undefined} />
        </Field>
        <Field label="Ngày chốt số dư đầu kỳ" hint="Không đổi được sau khi đã xác nhận số dư đầu kỳ." error={errors.opening_cutoff_date}>
          <DatePicker aria-label="Ngày chốt số dư đầu kỳ" value={draft.opening_cutoff_date} onChange={(v) => set("opening_cutoff_date", v)} invalid={errors.opening_cutoff_date !== undefined} today={today} />
        </Field>
        <StoreUrl />
      </FormGrid>
      <ButtonRow style={{ marginTop: 12 }}>
        <Button variant="primary" pending={commit.pending} disabled={readOnly.readOnly || !dirty} aria-describedby={readOnly.describedBy} onClick={save}>
          Lưu thông tin
        </Button>
      </ButtonRow>
      {readOnly.hint}
    </>
  );
}

/** "Thông tin cửa hàng": names, tax code and contact of the store, saved under If-Match. */
export function StoreProfileCard() {
  // The shell's GET /tenant: the API's ETag is the quoted version, so If-Match comes from it.
  const store = useTenant();
  return (
    <Card title="Thông tin cửa hàng" actions={store.data?.is_sandbox ? <StatusPill label="Sandbox" /> : null}>
      {store.data ? (
        <ProfileForm key={store.data.version} profile={store.data} />
      ) : store.error ? (
        <LoadError what="thông tin cửa hàng" error={store.error} />
      ) : (
        <ListLoading />
      )}
    </Card>
  );
}
