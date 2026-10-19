import { useState } from "react";
import { Link } from "react-router";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { fd } from "../../../shared/lib/format";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { PagedRows } from "../components/paged-rows";
import { StoreLink } from "../components/store-link";
import { TENANT_SEARCH_MAX_LENGTH, useTenants, type Filter } from "../api/queries";
import type { Tenant, TenantStatus } from "../api/schemas";
import { SUBSCRIPTION_STATUS, TENANT_STATUS, entriesOf } from "../lib/labels";

export const TENANTS_TITLE = "Cửa hàng";
export const TENANTS_TABLE = "Danh sách cửa hàng";
export const TENANT_SEARCH_PLACEHOLDER = "Tìm theo tên hoặc địa chỉ cửa hàng";

/** Cửa hàng (/admin/tenants): every store with its plan, subscription and lifecycle flags. */
export default function TenantsPage() {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<Filter<TenantStatus>>("");
  const search = useDebounce(q.trim());
  const tenants = useTenants(search, status);

  const columns: Column<Tenant>[] = [
    {
      key: "name",
      header: "Cửa hàng",
      cell: (t) => (
        <>
          <Link to={`/admin/tenants/${t.id}`}>{t.display_name}</Link>
          {t.is_sandbox ? <CellSub>Cửa hàng mẫu</CellSub> : null}
        </>
      ),
    },
    { key: "url", header: "Địa chỉ", cell: (t) => <StoreLink slug={t.slug} /> },
    { key: "plan", header: "Gói", cell: (t) => t.plan_name ?? "—" },
    {
      key: "sub",
      header: "Thuê bao",
      cell: (t) =>
        t.subscription_status ? (
          <>
            <StatusPill {...SUBSCRIPTION_STATUS[t.subscription_status]} />
            {t.current_period_end ? <CellSub>Đến {fd(t.current_period_end)}</CellSub> : null}
          </>
        ) : (
          "—"
        ),
    },
    { key: "status", header: "Trạng thái", cell: (t) => <StatusPill {...TENANT_STATUS[t.status]} /> },
    { key: "maintenance", header: "Bảo trì", cell: (t) => (t.maintenance ? "Đang bảo trì" : "—") },
    { key: "hold", header: "Giữ pháp lý", cell: (t) => (t.legal_hold ? "Đang giữ" : "—") },
  ];

  return (
    <>
      <PageHeader eyebrow="Quản trị nền tảng" title={TENANTS_TITLE} />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder={TENANT_SEARCH_PLACEHOLDER} maxLength={TENANT_SEARCH_MAX_LENGTH} />
        <select className="in" aria-label="Trạng thái cửa hàng" value={status} onChange={(e) => setStatus(e.target.value as Filter<TenantStatus>)}>
          <option value="">Mọi trạng thái</option>
          {entriesOf(TENANT_STATUS).map(([value, s]) => (
            <option key={value} value={value}>
              {s.label}
            </option>
          ))}
        </select>
      </FilterBar>
      <PagedRows list={tenants} what="danh sách cửa hàng">
        <DataTable caption={TENANTS_TABLE} columns={columns} rows={tenants.items} rowKey={(t) => t.id} empty="Không có cửa hàng khớp bộ lọc." />
      </PagedRows>
    </>
  );
}
