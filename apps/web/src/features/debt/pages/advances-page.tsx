import { useState } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { fd, money } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { canAllocate } from "../allocation";
import { PAYMENT_PURPOSE_LABELS } from "../api/adapters";
import { useAdvances } from "../api/queries";
import { AllocateAdvanceDialog } from "../containers/allocate-advance-dialog";
import { PaymentDrawer } from "../containers/payment-drawer";
import type { Payment, PaymentRow } from "../schemas";
import { useSearchParam } from "../use-search-param";

/** Ứng trước & cọc (/debts/advances). `?doc=PT-…|PC-…` opens that receipt or payment, allocated or not. */
export default function AdvancesPage() {
  const advances = useAdvances();
  const doc = useSearchParam("doc");
  const [allocating, setAllocating] = useState<{ payment: Payment; partyName: string } | null>(null);
  // A refund pays money back; it is not an advance to spread over documents.
  const rows = advances.items.filter((p) => p.purpose !== "refund");

  const columns: Column<PaymentRow>[] = [
    {
      key: "doc",
      header: "Phiếu",
      cell: (p) => (
        <>
          <DocNo value={p.doc_no} />
          <CellSub>{fd(p.business_date)}</CellSub>
        </>
      ),
    },
    { key: "party", header: "Đối tác", cell: (p) => p.party_name ?? "" },
    { key: "side", header: "Chiều", cell: (p) => (p.direction === "in" ? "Khách ứng trước" : "Cửa hàng ứng trước NCC") },
    { key: "kind", header: "Loại", cell: (p) => PAYMENT_PURPOSE_LABELS[p.purpose] ?? p.purpose },
    { key: "amount", header: "Số tiền", numeric: true, cell: (p) => money(p.amount) },
    { key: "unalloc", header: "Chưa phân bổ", numeric: true, cell: (p) => <b>{money(p.unallocated)}</b> },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (p) =>
        canAllocate(p) ? (
          <Button size="sm" aria-label={`Phân bổ ${p.doc_no}`} onClick={() => setAllocating({ payment: p, partyName: p.party_name ?? "" })}>
            Phân bổ
          </Button>
        ) : null,
    },
  ];

  return (
    <>
      <PageHeader lead="Tiền khách đặt cọc/ứng trước và tiền cửa hàng ứng trước NCC chưa phân bổ. Không phải doanh thu hay chi phí; phân bổ vào chứng từ khi có nghĩa vụ." />
      {advances.error ? (
        <Callout tone="bad" role="alert">
          {errorMessage(advances.error)}
        </Callout>
      ) : null}
      {advances.isLoading ? (
        <ListLoading />
      ) : (
        <DataTable caption="Ứng trước & cọc" columns={columns} rows={rows} rowKey={(p) => p.id} empty="Không có khoản ứng trước chưa phân bổ." />
      )}
      <ListFooter list={advances} />
      {doc.value !== null ? (
        <PaymentDrawer
          docNo={doc.value}
          onClose={doc.drop}
          onAllocate={(payment, partyName) => {
            doc.drop();
            setAllocating({ payment, partyName });
          }}
        />
      ) : null}
      {allocating !== null ? <AllocateAdvanceDialog payment={allocating.payment} partyName={allocating.partyName} onClose={() => setAllocating(null)} /> : null}
    </>
  );
}
