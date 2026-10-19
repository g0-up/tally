import { useState } from "react";
import { Link } from "react-router";
import { fdt } from "../../../shared/lib/dates";
import { num } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Stack } from "../../../shared/ui/grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyText } from "../../../shared/ui/money-text";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { usePartners, useReferrals } from "../api/queries";
import type { Partner, Referral } from "../api/schemas";
import { LoadError } from "../components/load-error";
import { PagedRows } from "../components/paged-rows";
import { CreatePartnerDialog } from "../containers/create-partner-dialog";
import { SUBSCRIPTION_STATUS } from "../lib/labels";

export const PARTNERS_TITLE = "Đối tác giới thiệu";
export const PARTNERS_TABLE = "Danh sách đối tác giới thiệu";

/** Đối tác giới thiệu (/admin/partners): referral partners, their codes and the stores they brought. */
export default function PartnersPage() {
  const partners = usePartners();
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState<Partner | null>(null);

  const columns: Column<Partner>[] = [
    { key: "name", header: "Đối tác", cell: (p) => <b>{p.name}</b> },
    { key: "code", header: "Mã giới thiệu", cell: (p) => <code>{p.referral_code}</code> },
    { key: "pct", header: "Hoa hồng", numeric: true, cell: (p) => `${num(p.commission_percent)}%` },
    { key: "refs", header: "Cửa hàng giới thiệu", numeric: true, cell: (p) => num(p.referrals) },
    { key: "active", header: "Trạng thái", cell: (p) => <StatusPill label={p.active ? "Đang hợp tác" : "Ngừng hợp tác"} tone={p.active ? "ok" : "mute"} /> },
    { key: "created", header: "Thêm lúc", cell: (p) => fdt(p.created_at) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (p) => (
        <Button size="sm" aria-label={`Xem cửa hàng do ${p.name} giới thiệu`} aria-pressed={selected?.id === p.id} onClick={() => setSelected(p)}>
          Xem giới thiệu
        </Button>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Quản trị nền tảng"
        title={PARTNERS_TITLE}
        lead="Hoa hồng chỉ tính trên hóa đơn thuê bao đã thanh toán, không tính trên doanh số của cửa hàng."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Thêm đối tác
          </Button>
        }
      />
      <Stack>
        <PagedRows list={partners} what="đối tác giới thiệu">
          <DataTable caption={PARTNERS_TABLE} columns={columns} rows={partners.items} rowKey={(p) => p.id} empty="Chưa có đối tác giới thiệu nào." />
        </PagedRows>
        {selected ? <Referrals partner={selected} /> : null}
      </Stack>
      {creating ? <CreatePartnerDialog onClose={() => setCreating(false)} /> : null}
    </>
  );
}

function Referrals({ partner }: { partner: Partner }) {
  const referrals = useReferrals(partner.id);
  const columns: Column<Referral>[] = [
    { key: "store", header: "Cửa hàng", cell: (r) => <Link to={`/admin/tenants/${r.tenant_id}`}>{r.display_name}</Link> },
    { key: "slug", header: "Địa chỉ", cell: (r) => r.slug },
    { key: "at", header: "Đăng ký lúc", cell: (r) => fdt(r.referred_at) },
    { key: "sub", header: "Thuê bao", cell: (r) => (r.subscription_status ? <StatusPill {...SUBSCRIPTION_STATUS[r.subscription_status]} /> : "—") },
    { key: "paid", header: "Đã thanh toán", numeric: true, cell: (r) => <MoneyText value={r.paid_invoices} /> },
    { key: "commission", header: "Hoa hồng", numeric: true, cell: (r) => <MoneyText value={r.commission} /> },
  ];
  const title = `Cửa hàng do ${partner.name} giới thiệu`;
  return (
    <Card title={title} note={`Mã ${partner.referral_code} · hoa hồng ${num(partner.commission_percent)}%`}>
      {referrals.isError ? <LoadError what="cửa hàng được giới thiệu" error={referrals.error} onRetry={() => void referrals.refetch()} /> : null}
      {referrals.isPending ? <ListLoading /> : referrals.data ? <DataTable flat caption={title} columns={columns} rows={referrals.data} rowKey={(r) => r.tenant_id} empty="Chưa có cửa hàng nào đăng ký bằng mã này." /> : null}
    </Card>
  );
}
