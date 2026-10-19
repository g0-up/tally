import { HttpResponse, http } from "msw";
import { apiUrl } from "../../../test/msw/handlers";
import type { Money } from "../../../shared/domain/money";
import type {
  DebtAdjustmentDetail,
  DebtOffsetDetail,
  DocumentSettlement,
  LedgerDocument,
  PartyLedger,
  Payment,
  PaymentDetail,
  PaymentRow,
  Statement,
  StatementRow,
  StatementVersion,
} from "../schemas";

// API payloads of the debt screens, shaped as OpenAPI returns them (the test tenant's business
// date is 2026-10-12).

export const CUSTOMER_ID = "cus-1";

/** Overrides as the API sends them: money as plain decimal strings. */
type Wire<T> = { [K in keyof T]?: T[K] extends Money ? string : T[K] extends Money | null | undefined ? string | null : T[K] extends (infer E)[] ? (E | Wire<E>)[] : T[K] };

/** The item at `i`, failing the test when the list is shorter. */
export function at<T>(items: readonly T[], i: number): T {
  const item = items[i];
  if (item === undefined) throw new Error(`No item at ${i} of ${items.length}`);
  return item;
}

export function testDocument(over: Wire<LedgerDocument> = {}): LedgerDocument {
  return {
    id: "obl-1",
    party_id: CUSTOMER_ID,
    direction: "receivable",
    source_type: "sales_document",
    source_id: "so-1",
    source_doc_no: "SO-0001",
    business_date: "2026-09-01",
    due_date: "2026-09-15",
    amount: "3000000",
    settled_amount: "0",
    open: "3000000",
    status: "open",
    reversed_on: null,
    version: 2,
    next_due: "2026-09-15",
    overdue: "3000000",
    ...over,
  } as unknown as LedgerDocument;
}

export function testPayment(over: Wire<Payment> = {}): Payment {
  return {
    id: "pay-1",
    doc_no: "PT-0001",
    direction: "in",
    party_id: CUSTOMER_ID,
    purpose: "advance",
    fund_account_id: "fund-1",
    payment_method_id: "pm-cash",
    amount: "1000000",
    allocated_amount: "0",
    unallocated: "1000000",
    status: "confirmed",
    business_date: "2026-10-01",
    reference: null,
    note: null,
    version: 1,
    ...over,
  } as unknown as Payment;
}

/** A row of `GET /payments`: the payment with its party and method names. */
export function testPaymentRow(over: Wire<PaymentRow> = {}): PaymentRow {
  return { ...testPayment(), party_name: "Tạp hóa Minh Châu", method_name: "Tiền mặt", ...over } as unknown as PaymentRow;
}

/** A settlement of SO-0001 by the payment pay-1. */
export function testSettlement(over: Wire<DocumentSettlement> = {}): DocumentSettlement {
  return {
    id: "set-1",
    obligation_id: "obl-1",
    obligation_doc_no: "SO-0001",
    settled_by_type: "payment",
    settled_by_id: "pay-1",
    amount: "500000",
    business_date: "2026-10-01",
    allocation_mode: "manual",
    reversal_of: null,
    posted_at: "2026-10-01T03:00:00Z",
    ...over,
  } as unknown as DocumentSettlement;
}

/** `GET /payments/{id}`: the row with the documents it settled. */
export function testPaymentDetail(over: Wire<PaymentDetail> = {}): PaymentDetail {
  return { ...testPaymentRow(), settlements: [], ...over } as unknown as PaymentDetail;
}

/** `GET /debt-adjustments/{id}`: a write-off of 200.000 ₫ on SO-0001. */
export function testAdjustmentDetail(over: Wire<DebtAdjustmentDetail> = {}): DebtAdjustmentDetail {
  return {
    id: "adj-1",
    doc_no: "DN-0001",
    party_id: CUSTOMER_ID,
    direction: "receivable",
    kind: "write_off",
    amount: "200000",
    business_date: "2026-10-10",
    note: "Khách xin bớt phần lẻ",
    party_name: "Tạp hóa Minh Châu",
    target_doc_no: "SO-0001",
    reason_name: "Bớt lẻ",
    obligation: null,
    settlements: [testSettlement({ id: "set-2", settled_by_type: "debt_adjustment", settled_by_id: "adj-1", amount: "200000", business_date: "2026-10-10" })],
    ...over,
  } as unknown as DebtAdjustmentDetail;
}

/** `GET /debt-offsets/{id}`: SO-0001 netted against the store's debt PN-0003 to the same party. */
export function testOffsetDetail(over: Wire<DebtOffsetDetail> = {}): DebtOffsetDetail {
  return {
    id: "off-1",
    doc_no: "BT-0001",
    party_id: CUSTOMER_ID,
    amount: "300000",
    business_date: "2026-10-11",
    note: null,
    party_name: "Tạp hóa Minh Châu",
    receivable_doc_no: "SO-0001",
    payable_doc_no: "PN-0003",
    settlements: [
      testSettlement({ id: "set-3", settled_by_type: "debt_offset", settled_by_id: "off-1", amount: "300000", business_date: "2026-10-11" }),
      testSettlement({ id: "set-4", obligation_id: "obl-9", obligation_doc_no: "PN-0003", settled_by_type: "debt_offset", settled_by_id: "off-1", amount: "300000", business_date: "2026-10-11" }),
    ],
    ...over,
  } as unknown as DebtOffsetDetail;
}

/** Two open invoices of Tạp hóa Minh Châu: SO-0002 is due before SO-0001. */
export function testLedger(over: Wire<PartyLedger> = {}): PartyLedger {
  return {
    party: { id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", phone: "0903123456" },
    receivable: "4500000",
    payable: "0",
    documents: [
      testDocument(),
      testDocument({ id: "obl-2", source_id: "so-2", source_doc_no: "SO-0002", business_date: "2026-08-20", due_date: "2026-09-05", next_due: "2026-09-05", amount: "1500000", open: "1500000", overdue: "1500000", version: 1 }),
    ],
    advances: [],
    pending_verification: [],
    ...over,
  } as unknown as PartyLedger;
}

export const PAYMENT_METHODS = [
  { id: "pm-cash", code: "cash", name: "Tiền mặt", fund_account_id: "fund-1", active: true },
  { id: "pm-bank", code: "bank", name: "Chuyển khoản", fund_account_id: "fund-2", active: true },
];

export function testStatement(over: Partial<Statement> = {}): Statement {
  return {
    id: "stm-1",
    doc_no: "DS-0001",
    party_id: CUSTOMER_ID,
    direction: "receivable",
    period_from: "2026-09-01",
    period_to: "2026-10-12",
    status: "draft",
    current_version: 1,
    sent_version: null,
    sent_at: null,
    dispute_note: null,
    created_at: "2026-10-12T02:00:00Z",
    version: 3,
    ...over,
  } as Statement;
}

/** A row of `GET /statements`: the statement, its party name and the current version's balances. */
export function testStatementRow(over: Wire<StatementRow> = {}): StatementRow {
  return { ...testStatement(), party_name: "Tạp hóa Minh Châu", opening_balance: "2000000", closing_balance: "4500000", ...over } as unknown as StatementRow;
}

export function testStatementVersion(over: Wire<StatementVersion> = {}): StatementVersion {
  return {
    statement_id: "stm-1",
    doc_no: "DS-0001",
    version_no: 1,
    status: "draft",
    opening_balance: "2000000",
    closing_balance: "4500000",
    snapshot: {
      party: { id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", phone: "0903123456" },
      direction: "receivable",
      period_from: "2026-09-01",
      period_to: "2026-10-12",
      opening_balance: "2000000",
      closing_balance: "4500000",
      lines: [
        { date: "2026-09-01", kind: "sales_document", doc_no: "SO-0001", due_date: "2026-09-15", increase: "3000000", decrease: "0", balance: "5000000" },
        { date: "2026-09-20", kind: "payment", doc_no: "PT-0003", due_date: null, increase: "0", decrease: "500000", balance: "4500000" },
      ],
    },
    created_at: "2026-10-12T02:00:00Z",
    ...over,
  } as unknown as StatementVersion;
}

/** Lookups every payment dialog reads. */
export const paymentLookups = [http.get(apiUrl("/payment-methods"), () => HttpResponse.json({ data: PAYMENT_METHODS }))];
