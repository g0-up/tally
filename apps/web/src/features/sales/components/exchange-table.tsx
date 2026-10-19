import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import type { Exchange } from "../schemas";

export interface ExchangeTableProps {
  exchanges: readonly Exchange[];
  customerName: (partyId: string | null) => string;
  /** Number of a loaded sale document by id. */
  docNoOf: (salesDocumentId: string | null) => string | null;
  /** Number of a loaded return by id. */
  returnNoOf: (salesReturnId: string) => string | null;
}

/** "Thu / trả khách": what the customer paid on top, or what the store paid back, and the old debt it cleared. */
function SettleCell({ x }: { x: Exchange }) {
  const debt = dec(x.debt_reduction_amount).gt(0) ? <CellSub>giảm nợ cũ {vnd(x.debt_reduction_amount)}</CellSub> : null;
  if (dec(x.customer_pays_amount).gt(0)) {
    return (
      <>
        {vnd(x.customer_pays_amount)}
        <CellSub>khách trả thêm</CellSub>
        {debt}
      </>
    );
  }
  if (dec(x.store_refunds_amount).gt(0)) {
    return (
      <>
        <span className="text-bad-ink">{vnd(x.store_refunds_amount)}</span>
        <CellSub>hoàn khách</CellSub>
        {debt}
      </>
    );
  }
  return (
    <>
      {vnd(0)}
      {debt}
    </>
  );
}

/** Prototype "Phiếu đổi hàng" table on the returns screen. */
export function ExchangeTable({ exchanges, customerName, docNoOf, returnNoOf }: ExchangeTableProps) {
  const columns: Column<Exchange>[] = [
    {
      key: "no",
      header: "Phiếu",
      cell: (x) => (
        <>
          <DocNo value={x.doc_no} />
          <CellSub>{fd(x.business_date)}</CellSub>
        </>
      ),
    },
    { key: "customer", header: "Khách", cell: (x) => customerName(x.customer_party_id) },
    { key: "source", header: "Chứng từ gốc", cell: (x) => <DocNo value={docNoOf(x.original_sales_document_id)} /> },
    {
      key: "returned",
      header: "Hàng trả",
      numeric: true,
      cell: (x) => (
        <>
          {vnd(x.returned_amount)}
          <CellSub>
            <DocNo value={returnNoOf(x.sales_return_id)} />
          </CellSub>
        </>
      ),
    },
    {
      key: "taken",
      header: "Hàng lấy",
      numeric: true,
      cell: (x) => (
        <>
          {vnd(x.taken_amount)}
          <CellSub>
            <DocNo value={docNoOf(x.new_sales_document_id)} />
          </CellSub>
        </>
      ),
    },
    { key: "settle", header: "Thu / trả khách", numeric: true, cell: (x) => <SettleCell x={x} /> },
  ];
  return <DataTable caption="Phiếu đổi hàng" columns={columns} rows={exchanges} rowKey={(x) => x.id} empty="Chưa có phiếu đổi hàng." />;
}
