import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage } from "../../../shared/api/api-error";
import type { Money } from "../../../shared/domain/money";
import { useCommit } from "../../../shared/hooks/use-commit";
import { dec } from "../../../shared/lib/decimal";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { Checkbox } from "../../../shared/ui/checkbox";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import {
  OFFLINE_HOURS_MAX,
  OFFLINE_HOURS_MIN,
  settingsKeys,
  tenantSettingsSchema,
  useTenantSettings,
  type OfflinePolicyPatch,
  type TenantSettings,
} from "../api/tenant-settings";
import { useReadOnlyHint } from "./read-only-hint";

export const OFFLINE_RISK_HINT =
  "Bán ngoại tuyến chỉ thu đủ tiền mặt và ghi sổ khi máy có mạng lại; giá, tồn và khuyến mãi được kiểm tra lại lúc đồng bộ. Hạn mức giới hạn số tiền chưa ghi sổ trên mỗi máy. Trên iPhone, iPad cần thêm ứng dụng vào Màn hình chính, nếu không trình duyệt có thể xóa dữ liệu ngoại tuyến.";

type PolicyField = "amount" | "hours";
const API_FIELDS: Readonly<Record<string, PolicyField>> = { offline_pos_max_amount: "amount", offline_pos_max_hours: "hours" };
const AMOUNT_ERROR = "Nhập hạn mức từ 0 đ trở lên.";
// A cap of 0 đ refuses every offline sale, so selling offline needs a cap above it.
const ENABLED_AMOUNT_ERROR = "Bật bán ngoại tuyến cần hạn mức lớn hơn 0 đ.";
const HOURS_ERROR = `Nhập số giờ nguyên từ ${OFFLINE_HOURS_MIN} đến ${OFFLINE_HOURS_MAX}.`;

function PolicyForm({ settings }: { settings: TenantSettings }) {
  const client = useApiClient();
  const readOnly = useReadOnlyHint();
  const [enabled, setEnabled] = useState(settings.offline_pos_enabled);
  const [amount, setAmount] = useState<Money | null>(settings.offline_pos_max_amount);
  const [hours, setHours] = useState(String(settings.offline_pos_max_hours));
  const [errors, setErrors] = useState<Partial<Record<PolicyField, string>>>({});

  const commit = useCommit({
    mutationFn: (patch: OfflinePolicyPatch) => client.patch("/tenant/settings", patch, { ifMatch: `"${settings.version}"`, schema: tenantSettingsSchema }),
    success: (saved) => (saved.offline_pos_enabled ? "Đã lưu chính sách bán ngoại tuyến. Máy bán hàng tải lại dữ liệu khi mở POS." : "Đã tắt bán ngoại tuyến."),
    // The POS bootstrap carries the same settings; its offline window follows them.
    invalidate: [settingsKeys.tenant(), ["pos"]],
    setFieldError: (field, message) => {
      const key = API_FIELDS[field];
      if (key) setErrors((cur) => ({ ...cur, [key]: message }));
    },
  });

  const save = () => {
    const next: Partial<Record<PolicyField, string>> = {};
    if (amount === null || dec(amount).lt(0)) next.amount = AMOUNT_ERROR;
    else if (enabled && dec(amount).lte(0)) next.amount = ENABLED_AMOUNT_ERROR;
    const h = Number(hours.trim());
    if (!/^\d+$/.test(hours.trim()) || h < OFFLINE_HOURS_MIN || h > OFFLINE_HOURS_MAX) next.hours = HOURS_ERROR;
    setErrors(next);
    if (amount === null || Object.keys(next).length > 0) return;
    void commit.run({ offline_pos_enabled: enabled, offline_pos_max_amount: amount, offline_pos_max_hours: h });
  };

  return (
    <>
      <Checkbox label="Bán ngoại tuyến tại quầy" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
      <FormGrid>
        <Field label="Hạn mức tiền mặt ngoại tuyến" hint="Tổng tiền các giao dịch chờ đồng bộ trên một máy." error={errors.amount}>
          <MoneyInput value={amount} onChange={setAmount} invalid={errors.amount !== undefined} />
        </Field>
        <Field label="Số giờ tối đa không đồng bộ" hint={`Từ ${OFFLINE_HOURS_MIN} đến ${OFFLINE_HOURS_MAX} giờ kể từ lần tải dữ liệu gần nhất.`} error={errors.hours}>
          <Input inputMode="numeric" value={hours} onChange={(e) => setHours(e.target.value)} invalid={errors.hours !== undefined} />
        </Field>
      </FormGrid>
      <Callout tone="warn">{OFFLINE_RISK_HINT}</Callout>
      <ButtonRow style={{ marginTop: 12 }}>
        <Button variant="primary" pending={commit.pending} disabled={readOnly.readOnly} aria-describedby={readOnly.describedBy} onClick={save}>
          Lưu
        </Button>
      </ButtonRow>
      {readOnly.hint}
    </>
  );
}

/** "Bán ngoại tuyến tại quầy" on /settings/account: the switch and the two risk caps of the store. */
export function OfflinePolicyCard() {
  const settings = useTenantSettings();
  return (
    <Card title="Chính sách bán ngoại tuyến">
      {settings.isPending ? (
        <ListLoading />
      ) : settings.error ? (
        <Callout tone="bad" role="alert">
          Không tải được cài đặt cửa hàng: {errorMessage(settings.error)}
        </Callout>
      ) : (
        <PolicyForm key={settings.data.version} settings={settings.data} />
      )}
    </Card>
  );
}
