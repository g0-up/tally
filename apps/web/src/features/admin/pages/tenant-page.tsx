import { useState } from "react";
import { useParams } from "react-router";
import { fdt } from "../../../shared/lib/dates";
import { fd, money, num } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Stack } from "../../../shared/ui/grid";
import { KeyValueGrid, type KeyValueItem } from "../../../shared/ui/key-value-grid";
import { TextLink } from "../../../shared/ui/link-button";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { useTenant } from "../api/queries";
import type { TenantDetail, Usage } from "../api/schemas";
import { LoadError } from "../components/load-error";
import { StoreLink } from "../components/store-link";
import { ChangePlanDialog, ExtendDialog, LegalHoldDialog, ReactivateDialog, RetryExportDialog, SuspendDialog } from "../containers/subscription-dialogs";
import { TenantFlags } from "../containers/tenant-flags";
import { CLOSURE_STATUS, EXPORT_STATUS, SUBSCRIPTION_STATUS, TENANT_STATUS } from "../lib/labels";
import { storageText } from "../lib/store-url";

type Action = "plan" | "extend" | "suspend" | "reactivate" | "hold" | "retry";

/** Chi tiết cửa hàng (/admin/tenants/:id): subscription, usage, account closure and the store's flags. */
export default function TenantPage() {
  const { id = "" } = useParams();
  const tenant = useTenant(id);
  const [action, setAction] = useState<Action | null>(null);

  if (tenant.isPending) return <ListLoading />;
  if (tenant.isError) {
    return (
      <>
        <PageHeader eyebrow="Cửa hàng" title="Chi tiết cửa hàng" />
        <LoadError what="cửa hàng" error={tenant.error} onRetry={() => void tenant.refetch()} />
      </>
    );
  }
  const t = tenant.data;
  const close = () => setAction(null);

  return (
    <>
      <PageHeader
        eyebrow="Cửa hàng"
        title={t.display_name}
        lead={<StoreLink slug={t.slug} />}
        actions={
          <>
            <TextLink to="/admin/tenants">Về danh sách</TextLink>
            <Button onClick={() => setAction("hold")}>{t.legal_hold ? "Gỡ giữ pháp lý" : "Bật giữ pháp lý"}</Button>
          </>
        }
      />
      <Stack>
        <SubscriptionCard tenant={t} onAction={setAction} />
        <UsageCard usage={t.usage} />
        <ClosureCard tenant={t} onRetry={() => setAction("retry")} />
        <TenantFlags store={{ id: t.id, slug: t.slug }} />
      </Stack>
      {action === "plan" ? <ChangePlanDialog tenant={t} onClose={close} /> : null}
      {action === "extend" ? <ExtendDialog tenant={t} onClose={close} /> : null}
      {action === "suspend" ? <SuspendDialog tenant={t} onClose={close} /> : null}
      {action === "reactivate" ? <ReactivateDialog tenant={t} onClose={close} /> : null}
      {action === "hold" ? <LegalHoldDialog tenant={t} onClose={close} /> : null}
      {action === "retry" ? <RetryExportDialog tenant={t} onClose={close} /> : null}
    </>
  );
}

function SubscriptionCard({ tenant, onAction }: { tenant: TenantDetail; onAction: (a: Action) => void }) {
  const { subscription: sub, plan } = tenant.subscription;
  const items: KeyValueItem[] = [
    { label: "Trạng thái cửa hàng", value: <StatusPill {...TENANT_STATUS[tenant.status]} /> },
    { label: "Bảo trì", value: tenant.maintenance ? "Đang bảo trì" : "Không" },
    { label: "Giữ pháp lý", value: tenant.legal_hold ? "Đang giữ" : "Không" },
    { label: "Tạo lúc", value: fdt(tenant.created_at) },
  ];
  if (sub) {
    items.push(
      { label: "Gói", value: plan ? `${plan.name} (${plan.code})` : "—" },
      { label: "Giá gói", value: plan ? money(plan.price) : "—" },
      { label: "Thuê bao", value: <StatusPill {...SUBSCRIPTION_STATUS[sub.status]} /> },
      { label: "Kỳ hiện tại", value: `${fd(sub.current_period_start)} – ${fd(sub.current_period_end)}` },
    );
    if (sub.trial_ends_on) items.push({ label: "Hết dùng thử", value: fd(sub.trial_ends_on) });
    if (sub.grace_ends_on) items.push({ label: "Hết ân hạn", value: fd(sub.grace_ends_on) });
    if (sub.read_only_ends_on) items.push({ label: "Hết chỉ xem", value: fd(sub.read_only_ends_on) });
    if (sub.cancel_at_period_end) items.push({ label: "Cuối kỳ", value: "Ngừng gia hạn" });
  }
  const suspended = sub?.status === "read_only";
  return (
    <Card
      title="Thuê bao"
      actions={
        sub && sub.status !== "closed" ? (
          <ButtonRow>
            <Button size="sm" onClick={() => onAction("plan")}>
              Đổi gói
            </Button>
            <Button size="sm" onClick={() => onAction("extend")}>
              Gia hạn
            </Button>
            {suspended ? (
              <Button size="sm" variant="primary" onClick={() => onAction("reactivate")}>
                Kích hoạt lại
              </Button>
            ) : (
              <Button size="sm" variant="danger" onClick={() => onAction("suspend")}>
                Tạm ngưng
              </Button>
            )}
          </ButtonRow>
        ) : undefined
      }
    >
      {tenant.is_sandbox ? <Callout>Đây là cửa hàng mẫu: không lập hóa đơn thuê bao.</Callout> : null}
      {sub ? null : <Callout tone="warn">Cửa hàng chưa có thuê bao.</Callout>}
      <KeyValueGrid items={items} />
    </Card>
  );
}

function UsageCard({ usage }: { usage: readonly Usage[] }) {
  const columns: Column<Usage>[] = [
    { key: "date", header: "Ngày", cell: (u) => fd(u.usage_date) },
    { key: "skus", header: "Mã hàng đang bán", numeric: true, cell: (u) => num(u.active_skus) },
    { key: "docs", header: "Chứng từ đã ghi sổ", numeric: true, cell: (u) => num(u.documents_posted) },
    { key: "storage", header: "Dung lượng", numeric: true, cell: (u) => storageText(u.storage_bytes) },
  ];
  return (
    <Card title="Mức sử dụng" note="Số liệu theo ngày gần nhất">
      <DataTable flat caption="Mức sử dụng của cửa hàng" columns={columns} rows={usage} rowKey={(u) => u.usage_date} empty="Chưa có số liệu sử dụng." />
    </Card>
  );
}

function ClosureCard({ tenant, onRetry }: { tenant: TenantDetail; onRetry: () => void }) {
  const request = tenant.closure?.request ?? null;
  const exp = tenant.closure?.export ?? null;
  if (!request && !exp) {
    return (
      <Card title="Đóng tài khoản">
        <p className="hint">Chủ cửa hàng chưa yêu cầu đóng tài khoản.</p>
      </Card>
    );
  }
  const items: KeyValueItem[] = [];
  if (request) {
    items.push(
      { label: "Yêu cầu", value: <StatusPill {...CLOSURE_STATUS[request.status]} /> },
      { label: "Gửi lúc", value: fdt(request.created_at) },
      { label: "Xác minh lúc", value: request.verified_at ? fdt(request.verified_at) : "—" },
      { label: "Chờ đến", value: fd(request.waiting_until) },
    );
  }
  if (exp) {
    items.push(
      { label: exp.final ? "Bản xuất cuối" : "Bản xuất", value: <StatusPill {...EXPORT_STATUS[exp.status]} /> },
      { label: "Số lần lỗi", value: num(exp.failed_attempts) },
      { label: "Hết hạn tải", value: exp.expires_at ? fdt(exp.expires_at) : "—" },
    );
  }
  const failed = request?.status === "export_failed";
  return (
    <Card
      title="Đóng tài khoản"
      actions={
        failed ? (
          <Button size="sm" variant="primary" onClick={onRetry}>
            Xuất lại dữ liệu
          </Button>
        ) : undefined
      }
    >
      {failed ? <Callout tone="bad">Bản xuất dữ liệu cuối bị lỗi; cửa hàng chưa đóng cho tới khi xuất thành công.</Callout> : null}
      <KeyValueGrid items={items} />
    </Card>
  );
}
