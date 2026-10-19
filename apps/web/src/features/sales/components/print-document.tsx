import { Fragment } from "react";
import { cx } from "../../../shared/lib/cx";
import { fdt } from "../../../shared/lib/dates";
import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { lineName } from "../api/adapters";
import type { PrintDoc, PrintLine } from "../schemas";

/** A5 sheet for deliveries and invoices, 80 mm roll for counter receipts. */
export type PaperSize = "a5" | "roll";

const CHILD_KINDS: ReadonlySet<PrintLine["line_kind"]> = new Set(["mix_flavor", "combo_component"]);

function LineRows({ lines }: { lines: readonly PrintLine[] }) {
  return lines.map((l) => {
    const child = CHILD_KINDS.has(l.line_kind);
    return (
      <tr key={l.line_no} className={cx(child && "pd-child")}>
        <td className="num pd-opt">{child ? "" : l.line_no}</td>
        <td>
          {lineName(l)}
          {l.line_kind === "gift" ? " (tặng)" : null}
          {l.color ? <span className="pd-sub"> · {l.color}</span> : null}
          {l.spec_text ? <span className="pd-sub pd-block">{l.spec_text}</span> : null}
        </td>
        <td className="pd-opt">{l.unit_name}</td>
        <td className="num">{num(l.qty)}</td>
        <td className="num">{vnd(l.unit_price)}</td>
        <td className="num pd-opt">{dec(l.discount_amount).gt(0) ? vnd(l.discount_amount) : ""}</td>
        <td className="num">{vnd(l.line_amount)}</td>
      </tr>
    );
  });
}

function LinesTable({ caption, lines }: { caption: string; lines: readonly PrintLine[] }) {
  return (
    <table className="pd-lines">
      <caption className="sr-only">{caption}</caption>
      <thead>
        <tr>
          <th scope="col" className="num pd-opt">STT</th>
          <th scope="col">Tên hàng</th>
          <th scope="col" className="pd-opt">ĐVT</th>
          <th scope="col" className="num">SL</th>
          <th scope="col" className="num">Đơn giá</th>
          <th scope="col" className="num pd-opt">CK</th>
          <th scope="col" className="num">Thành tiền</th>
        </tr>
      </thead>
      <tbody>
        <LineRows lines={lines} />
      </tbody>
    </table>
  );
}

/**
 * One printed document exactly as the print endpoint returns it: seller, customer, lines, totals
 * and the amounts the server listed. Nothing is recomputed here.
 */
export function PrintDocument({ doc, paper }: { doc: PrintDoc; paper: PaperSize }) {
  const t = doc.totals;
  const tenant = doc.tenant;
  const lines = doc.groups.flatMap((g) => g.lines);
  return (
    <article className={cx("print-doc", paper)} aria-label={`${doc.title} ${doc.doc_no}`}>
      {tenant ? (
        <header className="pd-seller">
          <b>{tenant.legal_name || tenant.name}</b>
          {tenant.address ? <span>{tenant.address}</span> : null}
          {tenant.phone || tenant.tax_code ? <span>{[tenant.phone ? `ĐT: ${tenant.phone}` : null, tenant.tax_code ? `MST: ${tenant.tax_code}` : null].filter(Boolean).join(" · ")}</span> : null}
        </header>
      ) : null}
      <h1 className="pd-title">{doc.title}</h1>
      <p className="pd-no">
        Số <b>{doc.doc_no}</b> · Ngày {fd(doc.date)}
      </p>
      {doc.label ? <p className="pd-label">{doc.label}</p> : null}
      <dl className="pd-party">
        <dt>Khách hàng</dt>
        <dd>{doc.customer?.name ?? "Khách lẻ"}</dd>
        {doc.customer?.phone ? (
          <>
            <dt>Điện thoại</dt>
            <dd>{doc.customer.phone}</dd>
          </>
        ) : null}
        {doc.customer?.address ? (
          <>
            <dt>Địa chỉ</dt>
            <dd>{doc.customer.address}</dd>
          </>
        ) : null}
        {doc.customer?.tax_code ? (
          <>
            <dt>MST</dt>
            <dd>{doc.customer.tax_code}</dd>
          </>
        ) : null}
      </dl>
      <LinesTable caption={`Dòng hàng ${doc.doc_no}`} lines={lines} />
      {doc.returned_lines && doc.returned_lines.length > 0 ? (
        <>
          <h2 className="pd-h">Hàng trả lại</h2>
          <LinesTable caption={`Hàng trả lại ${doc.doc_no}`} lines={doc.returned_lines} />
        </>
      ) : null}
      <dl className="pd-totals">
        <dt>Tiền hàng</dt>
        <dd>{vnd(t.goods_amount)}</dd>
        {dec(t.discount_amount).gt(0) ? (
          <>
            <dt>Chiết khấu</dt>
            <dd>{vnd(t.discount_amount)}</dd>
          </>
        ) : null}
        {dec(t.tax_amount).gt(0) ? (
          <>
            <dt>Thuế</dt>
            <dd>{vnd(t.tax_amount)}</dd>
          </>
        ) : null}
        <dt className="pd-grand">Tổng cộng</dt>
        <dd className="pd-grand">{vnd(t.total_amount)}</dd>
        {doc.amounts.map((a) => (
          <Fragment key={a.code}>
            <dt>{a.label}</dt>
            <dd>{vnd(a.amount)}</dd>
          </Fragment>
        ))}
        {doc.payment ? (
          <>
            <dt>Đã thanh toán</dt>
            <dd>{vnd(doc.payment.paid_amount)}</dd>
            <dt>Còn phải thu</dt>
            <dd>{vnd(doc.payment.open_amount)}</dd>
          </>
        ) : null}
      </dl>
      {doc.note ? <p className="pd-note">Ghi chú: {doc.note}</p> : null}
      <footer className="pd-foot">In lúc {fdt(doc.printed_at)}</footer>
    </article>
  );
}
