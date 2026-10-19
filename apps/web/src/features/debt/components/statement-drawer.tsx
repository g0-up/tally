import { Link } from "react-router";
import { errorMessage } from "../../../shared/api/api-error";
import { fdt } from "../../../shared/lib/dates";
import { dec } from "../../../shared/lib/decimal";
import { fd, money } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Timeline } from "../../../shared/ui/timeline";
import { STATEMENT_STATUS_LABELS, lineKindLabel } from "../api/adapters";
import type { StatementCommand } from "../api/mutations";
import { useStatementHistory, useStatementVersion } from "../api/queries";
import type { StatementLine, StatementRow } from "../schemas";

export interface StatementDrawerProps {
  statement: StatementRow;
  onClose: () => void;
  onCommand: (command: StatementCommand) => void;
  onDispute: () => void;
  pending: boolean;
}

type Row = { key: string; line: StatementLine | null; date: string; text: string };

/** Prototype drawSTM: the frozen lines of the current version, its status actions and history. */
export function StatementDrawer({ statement: s, onClose, onCommand, onDispute, pending }: StatementDrawerProps) {
  const version = useStatementVersion(s.id, s.current_version);
  const history = useStatementHistory(s);
  const v = version.data;

  const rows: Row[] = v
    ? [{ key: "opening", line: null, date: v.snapshot.period_from, text: "Số dư đầu kỳ" }, ...(v.snapshot.lines ?? []).map((line, i) => ({ key: String(i), line, date: line.date, text: lineKindLabel(line.kind) }))]
    : [];
  const columns: Column<Row>[] = [
    { key: "date", header: "Ngày", cell: (r) => fd(r.date) },
    { key: "doc", header: "Chứng từ", cell: (r) => (r.line ? <DocNo value={r.line.doc_no} /> : null), footer: "" },
    { key: "text", header: "Diễn giải", cell: (r) => r.text, footer: "Số dư cuối kỳ" },
    { key: "inc", header: "Phát sinh nợ", numeric: true, cell: (r) => (r.line && dec(r.line.increase).gt(0) ? money(r.line.increase) : "") },
    { key: "dec", header: "Thanh toán / giảm", numeric: true, cell: (r) => (r.line && dec(r.line.decrease).gt(0) ? money(r.line.decrease) : "") },
    { key: "bal", header: "Số dư", numeric: true, cell: (r) => money(r.line ? r.line.balance : (v?.opening_balance ?? "0")), footer: v ? money(v.closing_balance) : "" },
  ];

  const actions =
    s.status === "draft" ? (
      <Button variant="navy" size="sm" pending={pending} onClick={() => onCommand("send")}>
        Gửi khách
      </Button>
    ) : s.status === "sent" ? (
      <>
        <Button size="sm" pending={pending} onClick={() => onCommand("confirm")}>
          Khách xác nhận
        </Button>
        <Button variant="danger" size="sm" onClick={onDispute}>
          Tranh chấp
        </Button>
      </>
    ) : s.status === "disputed" ? (
      <Button variant="navy" size="sm" pending={pending} onClick={() => onCommand("generate")}>
        Lập phiên bản mới
      </Button>
    ) : null;

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Bảng kê ${s.doc_no} · v${s.current_version}`}
      sub={`${s.party_name} · ${fd(s.period_from)} – ${fd(s.period_to)}`}
      docKey={`${s.id}|${s.current_version}`}
    >
      <ButtonRow>
        <StatusPill label={STATEMENT_STATUS_LABELS[s.status]} />
        {actions}
        <Link className="btn btn-line btn-sm" to={`/print/statements/${s.id}?version=${s.current_version}`} target="_blank" rel="noopener">
          In bảng kê <span className="sr-only">(mở tab mới)</span>
        </Link>
      </ButtonRow>
      <Callout>Số liệu bảng kê được chụp tại lúc lập. Gửi qua cổng khách hàng bằng liên kết có thời hạn, thu hồi được.</Callout>
      {s.status === "disputed" && s.dispute_note ? <Callout tone="warn">Khách phản hồi: {s.dispute_note}</Callout> : null}
      {version.isError ? (
        <DrawerError message={errorMessage(version.error)} />
      ) : v === undefined ? (
        <ListLoading />
      ) : (
        <DataTable caption={`Bảng kê ${s.doc_no}`} columns={columns} rows={rows} rowKey={(r) => r.key} flat />
      )}
      <h3 className="sec-h">Lịch sử phiên bản</h3>
      <Timeline
        items={history.flatMap((q) =>
          q.data ? [{ key: String(q.data.version_no), time: fdt(q.data.created_at), content: `v${q.data.version_no} · ${STATEMENT_STATUS_LABELS[q.data.status]}` }] : [],
        )}
      />
    </DetailDrawer>
  );
}
