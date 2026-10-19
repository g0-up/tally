import { useState } from "react";
import { errorMessage, isApiError } from "../../../shared/api/api-error";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { fd, money } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { LinkButton } from "../../../shared/ui/link-button";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { DocNotFoundDrawer, useDocParam, useLoadAll } from "../../inventory";
import { STATEMENT_STATUS_LABELS } from "../api/adapters";
import { useStatementCommand } from "../api/mutations";
import { useCustomerProfile, useStatement, useStatements } from "../api/queries";
import { StatementDrawer } from "../components/statement-drawer";
import { DisputeDialog } from "../containers/dispute-dialog";
import { NewStatementDialog } from "../containers/new-statement-dialog";
import type { Statement, StatementRow } from "../schemas";
import { useSearchParam } from "../use-search-param";

/**
 * Đối soát (/debts/statements). `?doc=DS-…` or `?id=<statement id>` (a notification) opens a
 * statement, `?party=<id>` the new-statement dialog for that customer (the "Bảng kê" button of a
 * debt row). The list cannot be searched by number, so a `?doc=` link reads it to the end; an
 * `?id=` link reads that statement alone.
 */
export default function StatementsPage() {
  const today = useBusinessDate();
  const list = useStatements();
  const linkedId = useSearchParam("id");
  const linked = useStatement(linkedId.value);
  useLoadAll(list, useSearchParam("doc").value !== null);
  const docs = useDocParam(
    list.items.map((s) => ({ id: s.id, doc_no: s.doc_no })),
    !list.isLoading && !list.hasMore,
  );
  const party = useSearchParam("party");
  const preset = useCustomerProfile(party.value);
  const [creating, setCreating] = useState(false);
  const [disputing, setDisputing] = useState<Statement | null>(null);
  const command = useStatementCommand();

  const fromId = docs.id === null && linkedId.value !== null;
  const opened = fromId ? (list.items.find((s) => s.id === linkedId.value) ?? linked.data) : docs.id !== null ? list.items.find((s) => s.id === docs.id) : undefined;
  const idError = fromId && opened === undefined && linked.isError ? linked.error : null;
  const closeDrawer = () => {
    docs.close();
    if (linkedId.value !== null) linkedId.drop();
  };
  const run = (statement: Statement, c: "send" | "confirm" | "generate") => void command.run({ statement, command: c });

  const columns: Column<StatementRow>[] = [
    {
      key: "doc",
      header: "Bảng kê",
      cell: (r) => (
        <>
          <LinkButton onClick={() => docs.open(r.id)}>{r.doc_no}</LinkButton>
          <CellSub>
            v{r.current_version} · lập {fd(r.created_at.slice(0, 10))}
          </CellSub>
        </>
      ),
    },
    { key: "party", header: "Khách", cell: (r) => r.party_name },
    { key: "period", header: "Kỳ", cell: (r) => `${fd(r.period_from)} – ${fd(r.period_to)}` },
    { key: "opening", header: "Đầu kỳ", numeric: true, cell: (r) => money(r.opening_balance) },
    { key: "closing", header: "Cuối kỳ", numeric: true, cell: (r) => money(r.closing_balance) },
    { key: "status", header: "Trạng thái", cell: (r) => <StatusPill label={STATEMENT_STATUS_LABELS[r.status]} /> },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (s) =>
        s.status === "draft" ? (
          <Button variant="navy" size="sm" disabled={command.pending} aria-label={`Gửi khách ${s.doc_no}`} onClick={() => run(s, "send")}>
            Gửi khách
          </Button>
        ) : s.status === "sent" ? (
          <>
            <Button size="sm" disabled={command.pending} aria-label={`Khách xác nhận ${s.doc_no}`} onClick={() => run(s, "confirm")}>
              Khách xác nhận
            </Button>{" "}
            <Button variant="danger" size="sm" aria-label={`Tranh chấp ${s.doc_no}`} onClick={() => setDisputing(s)}>
              Tranh chấp
            </Button>
          </>
        ) : null,
    },
  ];

  const presetParty = party.value !== null && preset.data ? { id: preset.data.id, name: preset.data.name } : null;

  return (
    <>
      <PageHeader
        lead="Bảng kê có kỳ, số dư đầu, phát sinh và số dư cuối. Bản đã gửi là bất biến; điều chỉnh tạo phiên bản mới có lịch sử."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Lập bảng kê
          </Button>
        }
      />
      {list.isLoading ? <ListLoading /> : <DataTable caption="Bảng kê đối soát" columns={columns} rows={list.items} rowKey={(r) => r.id} />}
      <ListFooter list={list} />
      {opened !== undefined ? (
        <StatementDrawer statement={opened} onClose={closeDrawer} onCommand={(c) => run(opened, c)} onDispute={() => setDisputing(opened)} pending={command.pending} />
      ) : null}
      {docs.missing !== null ? <DocNotFoundDrawer title="Bảng kê đối soát" docNo={docs.missing} onClose={docs.close} /> : null}
      {idError !== null ? (
        <DetailDrawer open onClose={closeDrawer} title="Bảng kê đối soát">
          <DrawerError message={isApiError(idError) && idError.status === 404 ? "Không tìm thấy bảng kê này, có thể đã bị xóa. Tải lại để xem danh sách mới nhất." : errorMessage(idError)} />
        </DetailDrawer>
      ) : null}
      {creating || (party.value !== null && (preset.isSuccess || preset.isError)) ? (
        <NewStatementDialog
          today={today}
          party={creating ? null : presetParty}
          onClose={() => {
            setCreating(false);
            if (party.value !== null) party.drop();
          }}
          onCreated={(s) => docs.open(s.id)}
        />
      ) : null}
      {disputing !== null ? <DisputeDialog statement={disputing} onClose={() => setDisputing(null)} /> : null}
    </>
  );
}
