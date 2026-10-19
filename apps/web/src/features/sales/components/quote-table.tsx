import { fd, vnd } from "../../../shared/lib/format";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { StatusPill } from "../../../shared/ui/status-pill";
import { QUOTE_STATUS_LABELS } from "../api/adapters";
import { useInvoice } from "../api/queries";
import type { Quote } from "../schemas";
import { QuoteActions, type QuoteAction } from "./quote-actions";

export interface QuoteTableProps {
  quotes: readonly Quote[];
  customerName: (partyId: string) => string;
  /** The quote and action in flight. */
  pending: { id: string; action: QuoteAction } | null;
  onAction: (quote: Quote, action: QuoteAction) => void;
}

/** Number of the invoice a quote was converted into (the quote carries only its id). */
function ConvertedActions({ quote, pending, onAction }: { quote: Quote; pending: QuoteAction | null; onAction: (a: QuoteAction) => void }) {
  const invoice = useInvoice(quote.converted_invoice_id);
  return <QuoteActions status={quote.status} invoiceDocNo={invoice.data?.doc_no} pending={pending} onAction={onAction} />;
}

/** Prototype quotes view table: number with version and date, customer, validity, value, status, acts. */
export function QuoteTable({ quotes, customerName, pending, onAction }: QuoteTableProps) {
  const columns: Column<Quote>[] = [
    {
      key: "no",
      header: "Số",
      cell: (q) => (
        <>
          <DocNo value={q.doc_no} />
          <CellSub>
            v{q.revision_no} · {fd(q.quote_date)}
          </CellSub>
        </>
      ),
    },
    { key: "customer", header: "Khách hàng", cell: (q) => customerName(q.customer_party_id) },
    { key: "valid", header: "Hiệu lực đến", cell: (q) => fd(q.valid_until) },
    { key: "total", header: "Giá trị", numeric: true, cell: (q) => vnd(q.total_amount) },
    { key: "status", header: "Trạng thái", cell: (q) => <StatusPill label={QUOTE_STATUS_LABELS[q.status]} /> },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (q) => {
        const busy = pending?.id === q.id ? pending.action : null;
        const act = (a: QuoteAction) => onAction(q, a);
        return q.status === "converted" ? <ConvertedActions quote={q} pending={busy} onAction={act} /> : <QuoteActions status={q.status} pending={busy} onAction={act} />;
      },
    },
  ];
  return <DataTable caption="Báo giá" columns={columns} rows={quotes} rowKey={(q) => q.id} empty="Chưa có báo giá." />;
}
