import { useState } from "react";
import { money } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { useChangePlan, useExtendSubscription, useRetryClosureExport, useSetSubscriptionStatus } from "../api/mutations";
import { usePlans } from "../api/queries";
import type { TenantDetail } from "../api/schemas";
import { TypedConfirmDialog } from "../components/typed-confirm-dialog";

// Subscription and closure actions of the store detail. Each dialog is mounted only while open,
// so its fields start empty every time.

interface DialogProps {
  tenant: TenantDetail;
  onClose: () => void;
}

const SLUG_LABEL = "địa chỉ cửa hàng";

export function ChangePlanDialog({ tenant, onClose }: DialogProps) {
  const plans = usePlans();
  const currentId = tenant.subscription.plan?.id;
  const choices = (plans.data ?? []).filter((p) => p.active && p.id !== currentId);
  const [planId, setPlanId] = useState("");
  const [error, setError] = useState<string>();
  const chosen = choices.find((p) => p.id === planId);
  const change = useChangePlan(tenant.id, {
    success: () => (
      <>
        Đã đổi gói của <b>{tenant.slug}</b> sang <b>{chosen?.name}</b>.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    if (!chosen) return setError("Chọn gói mới.");
    void change.run({ plan_id: chosen.id });
  };
  return (
    <CommitDialog open onClose={onClose} title={`Đổi gói · ${tenant.display_name}`} sub={`Gói hiện tại: ${tenant.subscription.plan?.name ?? "—"}`} onSubmit={submit} submitLabel="Đổi gói" pending={change.pending}>
      <Field label="Gói mới" error={error} hint={plans.isPending ? "Đang tải danh sách gói…" : chosen ? `Giá ${money(chosen.price)}` : undefined}>
        <select
          className="in"
          value={planId}
          onChange={(e) => {
            setPlanId(e.target.value);
            setError(undefined);
          }}
        >
          <option value="">Chọn gói</option>
          {choices.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name} ({p.code})
            </option>
          ))}
        </select>
      </Field>
    </CommitDialog>
  );
}

const MAX_EXTEND_DAYS = 366;

export function ExtendDialog({ tenant, onClose }: DialogProps) {
  const [days, setDays] = useState("");
  const [error, setError] = useState<string>();
  const extend = useExtendSubscription(tenant.id, {
    success: () => (
      <>
        Đã gia hạn <b>{tenant.slug}</b> thêm {days.trim()} ngày.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    const text = days.trim();
    const n = /^\d+$/.test(text) ? Number.parseInt(text, 10) : NaN;
    if (!(n >= 1 && n <= MAX_EXTEND_DAYS)) return setError(`Nhập số ngày từ 1 đến ${MAX_EXTEND_DAYS}.`);
    void extend.run({ days: n });
  };
  return (
    <CommitDialog open onClose={onClose} title={`Gia hạn · ${tenant.display_name}`} sub="Kéo dài kỳ hiện tại của thuê bao, không lập hóa đơn." onSubmit={submit} submitLabel="Gia hạn" pending={extend.pending}>
      <Field label="Số ngày gia hạn" error={error}>
        <Input
          inputMode="numeric"
          value={days}
          invalid={error !== undefined}
          onChange={(e) => {
            setDays(e.target.value);
            setError(undefined);
          }}
        />
      </Field>
    </CommitDialog>
  );
}

export function SuspendDialog({ tenant, onClose }: DialogProps) {
  const setStatus = useSetSubscriptionStatus(tenant.id, {
    success: () => (
      <>
        Đã tạm ngưng <b>{tenant.slug}</b>: cửa hàng chỉ còn xem.
      </>
    ),
    after: onClose,
  });
  return (
    <TypedConfirmDialog
      open
      onClose={onClose}
      title={`Tạm ngưng · ${tenant.display_name}`}
      phrase={tenant.slug}
      phraseLabel={SLUG_LABEL}
      submitLabel="Tạm ngưng"
      pending={setStatus.pending}
      onConfirm={() => void setStatus.run({ status: "read_only" })}
    >
      <p>Cửa hàng chuyển sang chỉ xem: chủ cửa hàng vẫn đăng nhập và xem được dữ liệu, nhưng không ghi sổ được cho tới khi kích hoạt lại.</p>
    </TypedConfirmDialog>
  );
}

export function ReactivateDialog({ tenant, onClose }: DialogProps) {
  const setStatus = useSetSubscriptionStatus(tenant.id, {
    success: () => (
      <>
        Đã kích hoạt lại <b>{tenant.slug}</b>.
      </>
    ),
    after: onClose,
  });
  return (
    <CommitDialog open onClose={onClose} title={`Kích hoạt lại · ${tenant.display_name}`} onSubmit={() => void setStatus.run({ status: "active" })} submitLabel="Kích hoạt lại" pending={setStatus.pending}>
      <p>Thuê bao trở lại trạng thái đang dùng và cửa hàng ghi sổ lại được.</p>
    </CommitDialog>
  );
}

export function LegalHoldDialog({ tenant, onClose }: DialogProps) {
  const holding = tenant.legal_hold;
  const setStatus = useSetSubscriptionStatus(tenant.id, {
    success: () => (
      <>
        {holding ? "Đã gỡ giữ pháp lý" : "Đã bật giữ pháp lý"} cho <b>{tenant.slug}</b>.
      </>
    ),
    after: onClose,
  });
  const submit = () => void setStatus.run({ legal_hold: !holding });
  if (holding) {
    return (
      <TypedConfirmDialog open onClose={onClose} title={`Gỡ giữ pháp lý · ${tenant.display_name}`} phrase={tenant.slug} phraseLabel={SLUG_LABEL} submitLabel="Gỡ giữ pháp lý" pending={setStatus.pending} onConfirm={submit}>
        <p>Khi gỡ giữ pháp lý, yêu cầu đóng tài khoản đang chờ (nếu có) sẽ được thực hiện khi hết thời gian chờ.</p>
      </TypedConfirmDialog>
    );
  }
  return (
    <CommitDialog open onClose={onClose} title={`Bật giữ pháp lý · ${tenant.display_name}`} onSubmit={submit} submitLabel="Bật giữ pháp lý" pending={setStatus.pending}>
      <p>Cửa hàng đang giữ pháp lý không bị đóng khi hết thời gian chờ đóng tài khoản.</p>
    </CommitDialog>
  );
}

export function RetryExportDialog({ tenant, onClose }: DialogProps) {
  const retry = useRetryClosureExport(tenant.id, {
    success: () => (
      <>
        Đã bắt đầu xuất lại dữ liệu cuối của <b>{tenant.slug}</b>.
      </>
    ),
    after: onClose,
  });
  return (
    <CommitDialog open onClose={onClose} title={`Xuất lại dữ liệu cuối · ${tenant.display_name}`} onSubmit={() => void retry.run()} submitLabel="Xuất lại" pending={retry.pending}>
      <p>Bản xuất dữ liệu cuối đã lỗi {tenant.closure?.export?.failed_attempts ?? 0} lần. Xuất lại bắt đầu một lượt mới; cửa hàng đóng khi bản xuất thành công.</p>
    </CommitDialog>
  );
}
