import { useParams, useSearchParams } from "react-router";
import { errorMessage } from "../../../shared/api/api-error";
import { fdt } from "../../../shared/lib/dates";
import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { lineKindLabel } from "../api/adapters";
import { usePrintStatement } from "../api/queries";
import type { PrintStatement } from "../schemas";
import "../print.css";

function StatementSheet({ doc }: { doc: PrintStatement }) {
  const party = doc.customer ?? doc.supplier ?? null;
  const st = doc.statement;
  return (
    <article className="stp-doc" aria-label={`${doc.title} ${doc.doc_no}`}>
      {doc.tenant ? (
        <div className="stp-seller">
          <b>{doc.tenant.legal_name || doc.tenant.name}</b>
          {doc.tenant.address ? <span>{doc.tenant.address}</span> : null}
          {doc.tenant.tax_code ? <span>MST: {doc.tenant.tax_code}</span> : null}
          {doc.tenant.phone ? <span>ĐT: {doc.tenant.phone}</span> : null}
        </div>
      ) : null}
      <h1 className="stp-title">{doc.title}</h1>
      <p className="stp-no">
        Số {doc.doc_no}
        {doc.version != null ? ` · phiên bản ${doc.version}` : ""} · kỳ {fd(st.period_from)} – {fd(st.period_to)}
      </p>
      <dl className="stp-party">
        <dt>{doc.customer ? "Khách hàng" : "Nhà cung cấp"}</dt>
        <dd>{party?.name ?? "—"}</dd>
        {party?.address ? (
          <>
            <dt>Địa chỉ</dt>
            <dd>{party.address}</dd>
          </>
        ) : null}
        {party?.tax_code ? (
          <>
            <dt>MST</dt>
            <dd>{party.tax_code}</dd>
          </>
        ) : null}
        {party?.phone ? (
          <>
            <dt>Điện thoại</dt>
            <dd>{party.phone}</dd>
          </>
        ) : null}
      </dl>
      <table className="stp-lines">
        <caption className="sr-only">{doc.title}</caption>
        <thead>
          <tr>
            <th>Ngày</th>
            <th>Chứng từ</th>
            <th>Diễn giải</th>
            <th>Hạn</th>
            <th className="num">Phát sinh nợ</th>
            <th className="num">Thanh toán / giảm</th>
            <th className="num">Số dư</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>{fd(st.period_from)}</td>
            <td />
            <td>Số dư đầu kỳ</td>
            <td />
            <td />
            <td />
            <td className="num">{vnd(st.opening_balance)}</td>
          </tr>
          {(st.lines ?? []).map((l, i) => (
            <tr key={i}>
              <td>{fd(l.date)}</td>
              <td>{l.doc_no}</td>
              <td>{lineKindLabel(l.kind)}</td>
              <td>{l.due_date ? fd(l.due_date) : ""}</td>
              <td className="num">{dec(l.increase).gt(0) ? vnd(l.increase) : ""}</td>
              <td className="num">{dec(l.decrease).gt(0) ? vnd(l.decrease) : ""}</td>
              <td className="num">{vnd(l.balance)}</td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr>
            <td colSpan={6}>Số dư cuối kỳ</td>
            <td className="num">{vnd(st.closing_balance)}</td>
          </tr>
        </tfoot>
      </table>
      <div className="stp-sign">
        <div>
          <b>Đại diện khách hàng</b>
          <span>(Ký, ghi rõ họ tên)</span>
        </div>
        <div>
          <b>Đại diện cửa hàng</b>
          <span>(Ký, ghi rõ họ tên)</span>
        </div>
      </div>
      <p className="stp-foot">In lúc {fdt(doc.printed_at)}</p>
    </article>
  );
}

/** In bảng kê đối soát (/print/statements/:id?version=n): the server's print payload, A4, no shell. */
export default function PrintStatementPage() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const raw = params.get("version");
  const version = raw !== null && /^[1-9]\d*$/.test(raw) ? Number(raw) : null;
  const doc = usePrintStatement(id, version);

  return (
    <main className="stp-page">
      <div className="stp-bar">
        <span className="sp" />
        <Button variant="primary" size="sm" disabled={!doc.data} onClick={() => window.print()}>
          In phiếu
        </Button>
      </div>
      {doc.data ? (
        <StatementSheet doc={doc.data} />
      ) : doc.error ? (
        <Callout tone="bad" role="alert">
          Không tải được chứng từ để in: {errorMessage(doc.error)}
        </Callout>
      ) : (
        <div className="card" role="status" aria-live="polite">
          Đang tải…
        </div>
      )}
    </main>
  );
}
