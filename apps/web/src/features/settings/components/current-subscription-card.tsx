import { fd, num } from "../../../shared/lib/format";
import { Card } from "../../../shared/ui/card";
import { KeyValueGrid, type KeyValueItem } from "../../../shared/ui/key-value-grid";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { Plan, SubscriptionView } from "../../../shared/api/endpoints";
import { PLAN_KIND, PLAN_MODULE, SUBSCRIPTION_STATUS, priceText } from "../labels";

const LIMIT_UNITS: Readonly<Record<string, string>> = { skus: "SKU", storage_mb: "MB lưu trữ" };

function limitsText(limits: Readonly<Record<string, number>> | null | undefined): string {
  const entries = Object.entries(limits ?? {});
  if (entries.length === 0) return "Không giới hạn";
  return entries.map(([k, v]) => `${num(v)} ${LIMIT_UNITS[k] ?? k}`).join(" · ");
}

function afterExpiry(plan: Plan): string {
  const { grace_days: grace, read_only_days: readOnly } = plan.terms;
  const parts = [];
  if (grace > 0) parts.push(`Ân hạn ${grace} ngày`);
  parts.push(`Chỉ đọc ${readOnly} ngày, vẫn xuất được dữ liệu`);
  return parts.join(", rồi ");
}

function modulesText(modules: Readonly<Record<string, boolean>> | null): string {
  const on = Object.entries(modules ?? {})
    .filter(([, enabled]) => enabled)
    .map(([m]) => PLAN_MODULE[m] ?? m);
  return on.length > 0 ? on.join(", ") : "Chỉ module cơ bản";
}

/** "Thuê bao hiện tại": the plan, its period and the terms that apply when it ends. */
export function CurrentSubscriptionCard({ view, sandbox }: { view: SubscriptionView; sandbox: boolean }) {
  const { subscription, plan, entitlements } = view;
  const status = subscription ? SUBSCRIPTION_STATUS[subscription.status] : null;
  const items: KeyValueItem[] = [];
  if (subscription && plan) {
    items.push(
      { label: "Gói", value: plan.name },
      { label: "Loại", value: PLAN_KIND[plan.kind] },
      { label: "Hiệu lực", value: `${fd(subscription.current_period_start)} – ${fd(subscription.current_period_end)}` },
      { label: "Sau hết hạn", value: afterExpiry(plan) },
      { label: "Giá", value: priceText(plan) },
      { label: "Hạn mức", value: limitsText(entitlements.limits ?? plan.limits) },
      { label: "Module đang bật", value: modulesText(entitlements.modules) },
    );
    if (subscription.trial_ends_on) items.push({ label: "Hết dùng thử", value: fd(subscription.trial_ends_on) });
    if (subscription.grace_ends_on) items.push({ label: "Hết ân hạn", value: fd(subscription.grace_ends_on) });
    if (subscription.read_only_ends_on) items.push({ label: "Hết chỉ đọc", value: fd(subscription.read_only_ends_on) });
    if (subscription.cancel_at_period_end) items.push({ label: "Gia hạn", value: "Dừng khi hết kỳ" });
  }
  return (
    <Card
      title="Thuê bao hiện tại"
      actions={
        <>
          {status ? <StatusPill {...status} /> : null}
          {sandbox ? <StatusPill label="Sandbox" /> : null}
        </>
      }
    >
      {items.length > 0 ? <KeyValueGrid items={items} /> : <p className="hint">Cửa hàng chưa có thuê bao.</p>}
    </Card>
  );
}
