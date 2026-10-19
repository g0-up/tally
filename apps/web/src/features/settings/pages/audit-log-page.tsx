import { useMemo, useState } from "react";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { fdt } from "../../../shared/lib/dates";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { FilterBar, SearchInput } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useOwner } from "../api/account";
import { ACTOR_TYPE, AUDIT_PAGE_LIMIT, AUDIT_SEARCH_MAX_LENGTH, AUDIT_SOURCE, auditActionLabel, auditEntityLabel, auditEventSchema, type AuditEvent } from "../api/audit";
import { settingsKeys } from "../api/tenant-settings";
import { LoadError } from "../components/load-error";

export const AUDIT_LEAD = "Ghi lại tạo, sửa, duyệt, ghi sổ, đảo, xuất file và thay đổi quyền: ai, lúc nào, từ đâu. Nhật ký chỉ ghi thêm, không sửa xóa.";
export const AUDIT_SEARCH_PLACEHOLDER = "Tìm thao tác, chứng từ, người dùng…";
/** What the API's `q` matches: codes and names, not the Vietnamese labels the table shows. */
export const AUDIT_SEARCH_HINT = "Tìm theo số chứng từ, mã thao tác hoặc tên người.";

interface AuditRow {
  event: AuditEvent;
  action: string;
  actor: string;
  role: string;
  entity: string;
  detail: string;
}

function capitalise(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/**
 * Nhật ký (/settings/audit-log): the store's append-only log, newest first. The search box asks the
 * API (`q`); a new search starts again from the first page.
 */
export default function AuditLogPage() {
  const [q, setQ] = useState("");
  const search = useDebounce(q.trim(), 250);
  const owner = useOwner();
  const list = useCursorList({ queryKey: settingsKeys.auditEvents(), path: "/audit-events", item: auditEventSchema, params: { q: search }, limit: AUDIT_PAGE_LIMIT });

  const ownerId = owner.data?.user.id;
  const ownerName = owner.data?.user.full_name;
  const rows = useMemo<AuditRow[]>(
    () =>
      list.items.map((event) => {
        const role = ACTOR_TYPE[event.actor_type];
        const source = AUDIT_SOURCE[event.source] ?? event.source;
        return {
          event,
          action: auditActionLabel(event.action),
          actor: event.actor_type === "owner" && event.actor_id === ownerId && ownerName ? ownerName : role,
          role,
          entity: capitalise(auditEntityLabel(event.entity_type)),
          detail: event.ip ? `${source} · ${event.ip}` : source,
        };
      }),
    [list.items, ownerId, ownerName],
  );

  const columns: ReadonlyArray<Column<AuditRow>> = [
    {
      key: "at",
      header: "Thời điểm",
      cell: (r) => {
        const [date, time] = fdt(r.event.created_at).split(" ");
        return <CellText main={date} sub={time} />;
      },
    },
    { key: "actor", header: "Người dùng", cell: (r) => <CellText main={r.actor} sub={r.actor === r.role ? undefined : r.role} /> },
    { key: "action", header: "Thao tác", cell: (r) => <b style={{ color: "var(--navy-900)" }}>{r.action}</b> },
    { key: "doc", header: "Chứng từ", cell: (r) => (r.event.doc_no ? <DocNo value={r.event.doc_no} /> : r.entity) },
    { key: "detail", header: "Chi tiết", cell: (r) => r.detail },
  ];

  return (
    <>
      <PageHeader lead={AUDIT_LEAD} />
      <FilterBar>
        <SearchInput value={q} onChange={setQ} placeholder={AUDIT_SEARCH_PLACEHOLDER} label="Tìm trong nhật ký" maxLength={AUDIT_SEARCH_MAX_LENGTH} />
      </FilterBar>
      <p className="hint">{AUDIT_SEARCH_HINT}</p>
      {list.isLoading ? (
        <ListLoading />
      ) : list.error && list.items.length === 0 ? (
        <LoadError what="nhật ký" error={list.error} />
      ) : (
        <>
          <DataTable caption="Nhật ký" columns={columns} rows={rows} rowKey={(r) => r.event.id} empty={search !== "" ? "Không có dòng nào khớp." : "Chưa có thao tác nào."} />
          <ListFooter list={list} />
        </>
      )}
    </>
  );
}
