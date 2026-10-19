import { fdt } from "../../../shared/lib/dates";
import { fd, vnd } from "../../../shared/lib/format";
import { ButtonRow } from "../../../shared/ui/button";
import { DetailDrawer } from "../../../shared/ui/detail-drawer";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Timeline } from "../../../shared/ui/timeline";
import { QUOTE_STATUS_LABELS } from "../api/adapters";
import { useCustomerDirectory, useInvoice, useQuote, useQuoteHistory } from "../api/queries";
import { useQuoteActions } from "../hooks/use-quote-actions";
import type { QuoteHistoryEntry, QuoteLine } from "../schemas";
import { DocumentLines } from "./document-lines";
import { DrawerStatus } from "./drawer-status";
import { QuoteActions } from "./quote-actions";

const HISTORY_LABELS: Readonly<Record<string, string>> = {
  create: "tạo báo giá",
  created: "tạo báo giá",
  update: "sửa nháp",
  updated: "sửa nháp",
  send: "gửi khách",
  sent: "gửi khách",
  accept: "khách chấp nhận",
  accepted: "khách chấp nhận",
  reject: "khách từ chối",
  rejected: "khách từ chối",
  revise: "tạo phiên bản mới",
  revised: "tạo phiên bản mới",
  expire: "hết hạn",
  expired: "hết hạn",
  convert: "chuyển thành hóa đơn",
  converted: "chuyển thành hóa đơn",
  copy: "sao chép",
  copied: "sao chép",
};

/** "v2 · gửi khách": the version an audit entry left the quote at and what happened. */
function historyText(entry: QuoteHistoryEntry): string {
  const after = entry.after;
  const revision = typeof after === "object" && after !== null && "revision_no" in after && typeof after.revision_no === "number" ? after.revision_no : null;
  const action = entry.action.split(".").pop() ?? entry.action;
  const label = HISTORY_LABELS[action] ?? entry.action;
  return revision !== null ? `v${revision} · ${label}` : label;
}

const RETAIL_COLUMN = [{ key: "retail", header: "Giá bán lẻ", numeric: true, cell: (l: QuoteLine) => (l.retail_price !== null ? vnd(l.retail_price) : "—") }];

/** Prototype `openQuote`: the quote, its actions in its status, lines with retail prices and its history. */
export function QuoteDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const quote = useQuote(id);
  const history = useQuoteHistory(id);
  const customers = useCustomerDirectory();
  const actions = useQuoteActions();
  const q = quote.data;
  const invoice = useInvoice(q?.status === "converted" ? q.converted_invoice_id : null);

  if (!q) {
    return (
      <DetailDrawer open onClose={onClose} title="Báo giá" docKey={id}>
        <DrawerStatus error={quote.error} deletedText="Báo giá đã bị xóa." />
      </DetailDrawer>
    );
  }

  const busy = actions.pending?.id === q.id ? actions.pending.action : null;
  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Báo giá ${q.doc_no}`}
      sub={`${customers.name(q.customer_party_id)} · phiên bản ${q.revision_no} · hiệu lực đến ${fd(q.valid_until)}`}
      docKey={q.id}
    >
      <ButtonRow>
        <StatusPill label={QUOTE_STATUS_LABELS[q.status]} />
      </ButtonRow>
      <QuoteActions status={q.status} invoiceDocNo={invoice.data?.doc_no} pending={busy} onAction={(a) => actions.run(q, a)} />
      <DocumentLines caption={`Dòng hàng báo giá ${q.doc_no}`} lines={q.lines ?? []} spec extra={RETAIL_COLUMN} total={q.total_amount} />
      <ImpactSummary
        items={[
          { label: "Lãi gộp dự kiến", value: "Chưa có dữ liệu giá vốn" },
          { label: "Giữ tồn", value: "Không" },
          { label: "Doanh thu", value: "Chưa phát sinh" },
        ]}
      />
      {q.note ? <p className="hint">{q.note}</p> : null}
      <h3 className="sec-h">Lịch sử trạng thái &amp; phiên bản</h3>
      <Timeline items={(history.data?.data ?? []).map((h) => ({ key: h.id, time: fdt(h.created_at), content: historyText(h) }))} />
    </DetailDrawer>
  );
}
