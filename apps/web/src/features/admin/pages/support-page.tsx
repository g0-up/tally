import { useState } from "react";
import { Link } from "react-router";
import { fdt } from "../../../shared/lib/dates";
import { Card } from "../../../shared/ui/card";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { Stack } from "../../../shared/ui/grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { useSupportSessions, useTickets, type Filter } from "../api/queries";
import type { AdminTicket, SupportSession, TicketStatus } from "../api/schemas";
import { Assignee } from "../components/assignee";
import { LoadError } from "../components/load-error";
import { PagedRows } from "../components/paged-rows";
import { TenantPicker, type PickedTenant } from "../components/tenant-picker";
import { SESSION_STATE, SUPPORT_MODULE, TICKET_PRIORITY, TICKET_STATUS, entriesOf } from "../lib/labels";

export const SUPPORT_TITLE = "Hỗ trợ";
export const TICKETS_TABLE = "Yêu cầu hỗ trợ";
export const SESSIONS_TITLE = "Phiên hỗ trợ của tôi";

/** Hỗ trợ (/admin/support): store tickets and the support sessions owners granted to this admin. */
export default function SupportPage() {
  const [status, setStatus] = useState<Filter<TicketStatus>>("");
  const [tenant, setTenant] = useState<PickedTenant | null>(null);
  const tickets = useTickets(status, tenant?.id ?? "");

  const columns: Column<AdminTicket>[] = [
    {
      key: "number",
      header: "Yêu cầu",
      cell: (t) => (
        <>
          <Link to={`/admin/support/tickets/${t.id}`}>{t.number}</Link>
          <CellSub>{t.subject}</CellSub>
        </>
      ),
    },
    {
      key: "store",
      header: "Cửa hàng",
      cell: (t) => (
        <>
          {t.tenant_name}
          <CellSub>{t.tenant_slug}</CellSub>
        </>
      ),
    },
    { key: "status", header: "Trạng thái", cell: (t) => <StatusPill {...TICKET_STATUS[t.status]} /> },
    { key: "priority", header: "Ưu tiên", cell: (t) => TICKET_PRIORITY[t.priority] },
    { key: "sla", header: "Hạn xử lý", cell: (t) => (t.sla_due_at ? fdt(t.sla_due_at) : "—") },
    { key: "assigned", header: "Phân công", cell: (t) => <Assignee adminId={t.assigned_admin_id} /> },
    { key: "last", header: "Tin nhắn cuối", cell: (t) => (t.last_message_at ? fdt(t.last_message_at) : "—") },
  ];

  return (
    <>
      <PageHeader eyebrow="Quản trị nền tảng" title={SUPPORT_TITLE} />
      <Stack>
        <div>
          <FilterBar>
            <select className="in" aria-label="Trạng thái yêu cầu" value={status} onChange={(e) => setStatus(e.target.value as Filter<TicketStatus>)}>
              <option value="">Mọi trạng thái</option>
              {entriesOf(TICKET_STATUS).map(([v, s]) => (
                <option key={v} value={v}>
                  {s.label}
                </option>
              ))}
            </select>
            <TenantPicker aria-label="Cửa hàng" noneLabel="Mọi cửa hàng" value={tenant} onChange={setTenant} />
          </FilterBar>
          <PagedRows list={tickets} what="yêu cầu hỗ trợ">
            <DataTable caption={TICKETS_TABLE} columns={columns} rows={tickets.items} rowKey={(t) => t.id} empty="Không có yêu cầu khớp bộ lọc." />
          </PagedRows>
        </div>
        <SupportSessions />
      </Stack>
    </>
  );
}

function SupportSessions() {
  const sessions = useSupportSessions();
  const columns: Column<SupportSession>[] = [
    {
      key: "store",
      header: "Cửa hàng",
      cell: (s) => (
        <>
          {s.tenant_name}
          <CellSub>{s.tenant_slug}</CellSub>
        </>
      ),
    },
    {
      key: "modules",
      header: "Mục được xem",
      cell: (s) =>
        s.state === "active"
          ? s.modules.map((m, i) => (
              <span key={m}>
                {i > 0 ? ", " : null}
                <Link to={`/admin/support/tenants/${s.tenant_id}/${m}`}>{SUPPORT_MODULE[m]}</Link>
              </span>
            ))
          : s.modules.map((m) => SUPPORT_MODULE[m]).join(", "),
    },
    { key: "state", header: "Trạng thái", cell: (s) => <StatusPill {...SESSION_STATE[s.state]} /> },
    { key: "granted", header: "Cấp lúc", cell: (s) => fdt(s.granted_at) },
    { key: "expires", header: "Hết hạn", cell: (s) => fdt(s.revoked_at ?? s.expires_at) },
  ];
  return (
    <Card title={SESSIONS_TITLE} note="Chủ cửa hàng cấp phiên hỗ trợ cho từng mục; phiên chỉ cho xem, không cho ghi sổ.">
      {sessions.isError ? <LoadError what="phiên hỗ trợ" error={sessions.error} onRetry={() => void sessions.refetch()} /> : null}
      {sessions.isPending ? <ListLoading /> : sessions.data ? <DataTable flat caption={SESSIONS_TITLE} columns={columns} rows={sessions.data} rowKey={(s) => s.id} empty="Chưa có cửa hàng nào cấp phiên hỗ trợ cho bạn." /> : null}
    </Card>
  );
}
