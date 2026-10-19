import { errorMessage } from "../../../shared/api/api-error";
import { fd, money, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { canAllocate } from "../allocation";
import { PAYMENT_PURPOSE_LABELS, PAYMENT_STATUS_LABELS } from "../api/adapters";
import { usePaymentByNumber } from "../api/queries";
import type { DocumentSettlement, Payment } from "../schemas";

export interface PaymentDrawerProps {
  /** A PT or PC number. */
  docNo: string;
  onClose: () => void;
  /** "Phân bổ" of a confirmed payment with money left. */
  onAllocate: (payment: Payment, partyName: string) => void;
}

/** Prototype openPAY: a receipt or payment, its amounts and the documents it settled. */
export function PaymentDrawer({ docNo, onClose, onAllocate }: PaymentDrawerProps) {
  const payment = usePaymentByNumber(docNo);
  const p = payment.data;
  const title = `${docNo.startsWith("PC") ? "Phiếu chi" : "Phiếu thu"} ${docNo}`;

  // A reversal is its own negative row pointing at the allocation it undid; both say so, so the
  // pair does not read as the document settled twice.
  const reversed = new Set(p?.settlements.flatMap((s) => (s.reversal_of ? [s.reversal_of] : [])));
  const columns: Column<DocumentSettlement>[] = [
    {
      key: "doc",
      header: "Chứng từ",
      cell: (s) => (
        <>
          <DocNo value={s.obligation_doc_no} />
          {s.reversal_of ? <CellSub>Đảo phân bổ</CellSub> : reversed.has(s.id) ? <CellSub>Đã đảo</CellSub> : null}
        </>
      ),
    },
    { key: "date", header: "Ngày", cell: (s) => fd(s.business_date) },
    { key: "amount", header: "Số tiền", numeric: true, cell: (s) => money(s.amount) },
  ];

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={title}
      {...(p ? { sub: [p.party_name, fd(p.business_date), p.method_name].filter(Boolean).join(" · ") } : {})}
      docKey={docNo}
    >
      {payment.isError ? (
        <DrawerError message={errorMessage(payment.error)} />
      ) : p === undefined ? (
        <ListLoading />
      ) : p === null ? (
        <DrawerError message={`Không tìm thấy ${docNo}.`} />
      ) : (
        <>
          <ButtonRow>
            <StatusPill label={PAYMENT_STATUS_LABELS[p.status]} />
            {canAllocate(p) ? (
              <Button variant="primary" size="sm" onClick={() => onAllocate(p, p.party_name ?? "")}>
                Phân bổ
              </Button>
            ) : null}
          </ButtonRow>
          <ImpactSummary
            items={[
              { label: "Số tiền", value: vnd(p.amount) },
              { label: "Đã phân bổ", value: vnd(p.allocated_amount) },
              { label: "Chưa phân bổ", value: vnd(p.unallocated) },
              { label: "Loại", value: PAYMENT_PURPOSE_LABELS[p.purpose] ?? p.purpose },
            ]}
          />
          <DataTable caption={`Chứng từ ${p.doc_no} đã phân bổ`} columns={columns} rows={p.settlements} rowKey={(s) => s.id} empty="Không phân bổ." flat />
          {p.note ? <p className="hint">{p.note}</p> : null}
        </>
      )}
    </DetailDrawer>
  );
}
