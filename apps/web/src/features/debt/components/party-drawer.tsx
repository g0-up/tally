import { useBusinessDate } from "../../../shared/api/platform-queries";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { diffDays } from "../../../shared/lib/dates";
import { fd, money, vnd } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary, type ImpactItem } from "../../../shared/ui/impact-summary";
import { LinkButton } from "../../../shared/ui/link-button";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Timeline } from "../../../shared/ui/timeline";
import { errorMessage } from "../../../shared/api/api-error";
import { PAYMENT_PURPOSE_LABELS, PAYMENT_STATUS_LABELS, SIDE } from "../api/adapters";
import { useCollectionNotes, useCustomerProfile, usePartyLedger, useSupplierProfile } from "../api/queries";
import type { DebtDirection, LedgerDocument, Payment } from "../schemas";

export interface PartyDrawerProps {
  partyId: string;
  direction: DebtDirection;
  onClose: () => void;
  onPay: () => void;
  onStatement: () => void;
  onNote: () => void;
  onWriteOff: (doc: LedgerDocument) => void;
}

/** Prototype openParty: one ledger of a partner with its open documents, payments and contact notes. */
export function PartyDrawer({ partyId, direction, onClose, onPay, onStatement, onNote, onWriteOff }: PartyDrawerProps) {
  const side = SIDE[direction];
  const today = useBusinessDate();
  const ledger = usePartyLedger(partyId);
  const notes = useCollectionNotes(direction === "receivable" ? partyId : null);
  const customer = useCustomerProfile(direction === "receivable" ? partyId : null);
  const supplier = useSupplierProfile(direction === "payable" ? partyId : null);

  const l = ledger.data;
  const paymentDirection = side.paymentDirection;
  const docs = (l?.documents ?? []).filter((d) => d.direction === direction).sort((a, b) => b.business_date.localeCompare(a.business_date));
  const advances = (l?.advances ?? []).filter((p) => p.direction === paymentDirection);
  const payments = [...advances, ...(l?.pending_verification ?? []).filter((p) => p.direction === paymentDirection)];
  const owes = l ? (direction === "receivable" ? l.receivable : l.payable) : "0";
  const advance = sumDec(advances.map((p) => p.unallocated));

  const group = direction === "receivable" ? customer.data?.customer_group_name : null;
  const sub = [group, l?.party.phone, side.ledger].filter((x): x is string => !!x).join(" · ");
  const bothSides = direction === "receivable" ? customer.data?.is_supplier === true : supplier.data?.is_customer === true;

  const lastItem: ImpactItem =
    direction === "receivable"
      ? {
          label: "Hạn mức còn lại",
          value: customer.data?.credit_limit != null ? vnd(dec(customer.data.credit_limit).minus(owes)) : "Không giới hạn",
        }
      : { label: "Điều khoản", value: supplier.data?.payment_terms_days != null ? `${supplier.data.payment_terms_days} ngày` : "—" };

  const docColumns: Column<LedgerDocument>[] = [
    {
      key: "doc",
      header: "Chứng từ",
      cell: (d) => (
        <>
          <DocNo value={d.source_doc_no} />
          {d.source_type === "opening_balance" ? (
            <>
              {" "}
              <Badge alt>đầu kỳ</Badge>
            </>
          ) : null}
        </>
      ),
    },
    { key: "date", header: "Ngày", cell: (d) => fd(d.business_date) },
    {
      key: "due",
      header: "Hạn",
      cell: (d) => (
        <>
          {fd(d.next_due)}
          {dec(d.open).gt(0) && d.next_due < today ? (
            <>
              {" "}
              <StatusPill label={`Quá hạn ${diffDays(today, d.next_due)} ngày`} tone="bad" />
            </>
          ) : null}
        </>
      ),
    },
    { key: "amount", header: "Phát sinh", numeric: true, cell: (d) => money(d.amount) },
    { key: "open", header: "Còn lại", numeric: true, cell: (d) => money(d.open) },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (d) =>
        direction === "receivable" && dec(d.open).gt(0) ? (
          <LinkButton aria-label={`Giảm/xóa nợ ${d.source_doc_no}`} onClick={() => onWriteOff(d)}>
            Giảm/xóa nợ
          </LinkButton>
        ) : null,
    },
  ];
  const payColumns: Column<Payment>[] = [
    { key: "doc", header: "Phiếu", cell: (p) => <DocNo value={p.doc_no} /> },
    { key: "date", header: "Ngày", cell: (p) => fd(p.business_date) },
    { key: "kind", header: "Loại", cell: (p) => PAYMENT_PURPOSE_LABELS[p.purpose] ?? p.purpose },
    { key: "amount", header: "Số tiền", numeric: true, cell: (p) => money(p.amount) },
    { key: "unalloc", header: "Chưa phân bổ", numeric: true, cell: (p) => money(p.unallocated) },
    { key: "status", header: <span className="sr-only">Trạng thái</span>, cell: (p) => <StatusPill label={PAYMENT_STATUS_LABELS[p.status]} /> },
  ];

  return (
    <DetailDrawer open onClose={onClose} title={l?.party.name ?? (ledger.isError ? "Công nợ" : "Công nợ · Đang tải…")} sub={sub} docKey={`${partyId}|${direction}`}>
      {ledger.isError ? (
        <DrawerError message={errorMessage(ledger.error)} />
      ) : l === undefined ? (
        <ListLoading />
      ) : (
        <>
          <ButtonRow>
            <Button variant="primary" size="sm" onClick={onPay}>
              {side.pay}
            </Button>
            {direction === "receivable" ? (
              <>
                <Button onClick={onStatement}>Lập bảng kê</Button>
                <Button onClick={onNote}>Ghi chú nhắc nợ</Button>
              </>
            ) : null}
          </ButtonRow>
          <ImpactSummary
            items={[
              { label: side.owes, value: vnd(owes) },
              { label: "Ứng trước chưa phân bổ", value: vnd(advance) },
              { label: "Số dư ròng (giải thích)", value: vnd(dec(owes).minus(advance)) },
              lastItem,
            ]}
          />
          {bothSides ? <Callout>Đối tác này vừa là khách vừa là NCC. Hai sổ tách biệt; bù trừ chỉ thực hiện bằng biên bản.</Callout> : null}
          <h3 className="sec-h">Chứng từ còn mở</h3>
          <DataTable caption="Chứng từ còn mở" columns={docColumns} rows={docs} rowKey={(d) => d.id} flat />
          <h3 className="sec-h">Thanh toán</h3>
          <DataTable caption="Thanh toán" columns={payColumns} rows={payments} rowKey={(p) => p.id} flat empty="Chưa có." />
          {notes.items.length > 0 ? (
            <>
              <h3 className="sec-h">Ghi chú liên hệ</h3>
              <Timeline
                items={notes.items.map((n) => ({
                  key: n.id,
                  time: fd(n.created_at.slice(0, 10)),
                  content: `${n.body ?? ""}${n.promised_date ? ` · hẹn trả ${fd(n.promised_date)}` : ""}`,
                }))}
              />
            </>
          ) : null}
        </>
      )}
    </DetailDrawer>
  );
}
