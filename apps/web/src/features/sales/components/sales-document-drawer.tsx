import { Link } from "react-router";
import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import { SALES_DOC_SOURCE_LABELS, SALES_DOC_STATUS_LABELS } from "../api/adapters";
import { useCustomerDirectory, useExchangeList, useInvoice, useSalesDocument, useSalesReturnList } from "../api/queries";
import { useSalesOverlay } from "../hooks/use-sales-overlay";
import type { SalesReturn } from "../schemas";
import { DocumentLines } from "./document-lines";
import { DrawerStatus } from "./drawer-status";

const RETURN_COLUMNS: Column<SalesReturn>[] = [
  { key: "no", header: "Phiếu", cell: (r) => <DocNo value={r.doc_no} /> },
  { key: "value", header: "Giá trị", numeric: true, cell: (r) => vnd(r.total_amount) },
];

/**
 * Prototype `drawSaleDoc`: a posted sale (counter sale or delivery) with its lines, the receivable it
 * raised and the returns made against it; "Đổi hàng" and "Trả hàng" open the prefilled dialogs.
 */
export function SalesDocumentDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const overlay = useSalesOverlay();
  const query = useSalesDocument(id);
  const customers = useCustomerDirectory();
  const returns = useSalesReturnList();
  const exchanges = useExchangeList();
  const doc = query.data;
  const invoice = useInvoice(doc?.invoice_id ?? null);

  if (!doc) {
    return (
      <DetailDrawer open onClose={onClose} title="Chứng từ bán" docKey={id}>
        <DrawerStatus error={query.error} deletedText="Chứng từ bán không còn." />
      </DetailDrawer>
    );
  }

  const retail = doc.source === "pos" || doc.source === "pos_offline";
  const fromExchange = exchanges.items.find((x) => x.new_sales_document_id === doc.id);
  const docReturns = returns.items.filter((r) => r.sales_document_id === doc.id && r.status === "posted");
  const m = doc.payment;
  const settled = dec(m.total_amount).minus(m.open_amount);
  const due = invoice.data?.due_date ?? null;
  const reopen = (kind: "return" | "exchange") => {
    overlay.closeDrawer();
    overlay.openDialog({ kind, documentId: doc.id });
  };

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`${retail ? "Hóa đơn bán lẻ" : "Chứng từ bán nội bộ"} ${doc.doc_no}`}
      sub={`${customers.name(doc.customer_party_id)} · ${fd(doc.business_date)} · ${SALES_DOC_SOURCE_LABELS[doc.source]}`}
      docKey={doc.id}
    >
      <ButtonRow>
        <StatusPill label={SALES_DOC_STATUS_LABELS[doc.status]} />
        {doc.status !== "reversed" ? (
          <>
            <Button variant="navy" size="sm" onClick={() => reopen("exchange")}>
              Đổi hàng
            </Button>
            <Button size="sm" onClick={() => reopen("return")}>
              Trả hàng
            </Button>
          </>
        ) : null}
        <Link className="btn btn-line btn-sm" to={`/print/sales-documents/${doc.id}`} target="_blank" rel="noopener">
          In phiếu
        </Link>
        {fromExchange ? <span className="tag">từ phiếu đổi {fromExchange.doc_no}</span> : null}
      </ButtonRow>
      <Callout tone="info">Chứng từ bán nội bộ, không phải hóa đơn điện tử. Xuất HĐĐT qua đối tác sẽ liên kết vào chứng từ này, không tạo thêm doanh thu.</Callout>
      <DocumentLines caption={`Dòng hàng của ${doc.doc_no}`} lines={doc.lines ?? []} total={doc.total_amount} />
      <h3 className="sec-h">Phải thu</h3>
      <ImpactSummary
        items={[
          { label: "Nghĩa vụ", value: vnd(m.total_amount) },
          { label: "Đã thanh toán / giảm", value: vnd(settled.gt(0) ? settled : 0) },
          { label: "Còn lại", value: vnd(m.open_amount) },
          { label: "Hạn", value: due ? fd(due) : "—" },
        ]}
      />
      <h3 className="sec-h">Trả hàng</h3>
      <DataTable caption={`Phiếu trả của ${doc.doc_no}`} columns={RETURN_COLUMNS} rows={docReturns} rowKey={(r) => r.id} flat empty="Chưa có phiếu trả nào." />
    </DetailDrawer>
  );
}
