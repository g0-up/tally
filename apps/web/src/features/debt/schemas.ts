import { z } from "zod";
import { moneySchema } from "../../shared/api/decimal-schemas";
import type { components } from "../../shared/api/schema";

// Response schemas of the debt endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped. The payment, statement and
// adjustment schemas `satisfy` the generated types over the fields they read, so a renamed or
// retyped field breaks the typecheck.

type S = components["schemas"];

const id = z.string();
const nid = z.string().nullish();
const ntext = z.string().nullish();

export const directionSchema = z.enum(["receivable", "payable"]);
export type DebtDirection = z.infer<typeof directionSchema>;

/* ---------- parties and ledgers ---------- */

/** One row of `GET /debts/parties`: a party with open documents or an advance on one ledger. */
export const partySummarySchema = z.object({
  party_id: id,
  name: z.string(),
  phone: ntext,
  direction: directionSchema,
  open: moneySchema,
  overdue: moneySchema,
  oldest_overdue_due: nid,
  open_documents: z.number().int(),
  advance: moneySchema,
});
export type PartySummary = z.infer<typeof partySummarySchema>;

/** A settlement of a document, with the number of the document it settled. */
export const documentSettlementSchema = z.object({
  id,
  obligation_id: id,
  obligation_doc_no: z.string(),
  settled_by_type: z.enum(["payment", "credit_note", "debt_offset", "debt_adjustment", "obligation_reversal"]),
  settled_by_id: id,
  amount: moneySchema,
  business_date: z.string(),
  allocation_mode: z.enum(["manual", "suggested_oldest_due", "auto_earmarked"]),
  reversal_of: id.nullable(),
  posted_at: z.string(),
}) satisfies z.ZodType<S["DocumentSettlement"]>;
export type DocumentSettlement = z.infer<typeof documentSettlementSchema>;

export const ledgerDocumentSchema = z.object({
  id,
  party_id: id,
  direction: directionSchema,
  source_type: z.string(),
  source_id: id,
  source_doc_no: z.string(),
  business_date: z.string(),
  due_date: z.string(),
  amount: moneySchema,
  settled_amount: moneySchema,
  open: moneySchema,
  status: z.enum(["open", "settled"]),
  reversed_on: nid,
  version: z.number().int(),
  next_due: z.string(),
  overdue: moneySchema,
});
export type LedgerDocument = z.infer<typeof ledgerDocumentSchema>;

export const paymentPurposeSchema = z.enum(["settlement", "advance", "deposit", "refund", "advance_refund", "opening_advance"]) satisfies z.ZodType<S["PaymentPurpose"]>;

/** Fields of a payment the generated type is checked over (the optional ones are left out). */
type PaymentFields = "id" | "doc_no" | "direction" | "party_id" | "purpose" | "fund_account_id" | "payment_method_id" | "amount" | "allocated_amount" | "unallocated" | "status" | "business_date" | "version";

export const paymentSchema = z.object({
  id,
  doc_no: z.string(),
  direction: z.enum(["in", "out"]),
  party_id: id.nullable(),
  purpose: paymentPurposeSchema,
  fund_account_id: id,
  payment_method_id: id,
  amount: moneySchema,
  allocated_amount: moneySchema,
  unallocated: moneySchema,
  status: z.enum(["pending_verification", "confirmed", "reversed"]),
  business_date: z.string(),
  reference: ntext,
  note: ntext,
  version: z.number().int(),
}) satisfies z.ZodType<Pick<S["Payment"], PaymentFields>>;
export type Payment = z.infer<typeof paymentSchema>;

/** One row of `GET /payments`: the payment with its party and method names. */
export const paymentRowSchema = paymentSchema.extend({
  party_name: z.string().nullable(),
  method_name: z.string(),
}) satisfies z.ZodType<Pick<S["PendingPayment"], PaymentFields | "party_name" | "method_name">>;
export type PaymentRow = z.infer<typeof paymentRowSchema>;

/** `GET /payments/{id}`: the row with the documents it settled. */
export const paymentDetailSchema = paymentRowSchema.extend({
  settlements: z.array(documentSettlementSchema),
}) satisfies z.ZodType<Pick<S["PaymentDetail"], PaymentFields | "party_name" | "method_name" | "settlements">>;
export type PaymentDetail = z.infer<typeof paymentDetailSchema>;

export const partyLedgerSchema = z.object({
  party: z.object({ id, name: z.string(), phone: ntext }),
  receivable: moneySchema,
  payable: moneySchema,
  documents: z.array(ledgerDocumentSchema),
  advances: z.array(paymentSchema),
  pending_verification: z.array(paymentSchema),
});
export type PartyLedger = z.infer<typeof partyLedgerSchema>;

/* ---------- aging and forecast ---------- */

export const bucketsSchema = z.object({
  current: moneySchema,
  d1_30: moneySchema,
  d31_60: moneySchema,
  d61_90: moneySchema,
  over_90: moneySchema,
  total: moneySchema,
});
export type AgingBuckets = z.infer<typeof bucketsSchema>;

export const agingSchema = z.object({
  direction: directionSchema,
  as_of: z.string(),
  rows: z.array(z.object({ party_id: id, name: z.string(), buckets: bucketsSchema })),
  totals: bucketsSchema,
});
export type DebtAging = z.infer<typeof agingSchema>;

export const forecastSchema = z.object({
  from: z.string(),
  to: z.string(),
  overdue_receivable: moneySchema,
  overdue_payable: moneySchema,
  days: z.array(
    z.object({
      date: z.string(),
      receivable_due: moneySchema,
      payable_due: moneySchema,
      promised: moneySchema,
      confirmed_in: moneySchema,
      confirmed_out: moneySchema,
    }),
  ),
});
export type DebtForecast = z.infer<typeof forecastSchema>;

/* ---------- collection notes ---------- */

export const collectionNoteSchema = z.object({
  id,
  party_id: id,
  direction: directionSchema,
  kind: z.string(),
  body: ntext,
  promised_date: nid,
  promised_amount: moneySchema.nullish(),
  channel: ntext,
  created_at: z.string(),
});
export type CollectionNote = z.infer<typeof collectionNoteSchema>;

/* ---------- statements ---------- */

export const statementStatusSchema = z.enum(["draft", "sent", "confirmed", "disputed", "superseded"]);
export type StatementStatus = z.infer<typeof statementStatusSchema>;

export const statementSchema = z.object({
  id,
  doc_no: z.string(),
  party_id: id,
  direction: directionSchema,
  period_from: z.string(),
  period_to: z.string(),
  status: statementStatusSchema,
  current_version: z.number().int(),
  sent_version: z.number().int().nullable(),
  sent_at: z.string().nullable(),
  dispute_note: z.string().nullable(),
  created_at: z.string(),
  version: z.number().int(),
}) satisfies z.ZodType<S["Statement"]>;
export type Statement = z.infer<typeof statementSchema>;

/** One row of `GET /statements` and `GET /statements/{id}`: the party name and the current version's balances. */
export const statementRowSchema = statementSchema.extend({
  party_name: z.string(),
  opening_balance: moneySchema,
  closing_balance: moneySchema,
}) satisfies z.ZodType<S["StatementRow"]>;
export type StatementRow = z.infer<typeof statementRowSchema>;

export const statementLineSchema = z.object({
  date: z.string(),
  kind: z.string(),
  doc_no: z.string(),
  due_date: nid,
  increase: moneySchema,
  decrease: moneySchema,
  balance: moneySchema,
});
export type StatementLine = z.infer<typeof statementLineSchema>;

/** The frozen content of one statement version. */
export const statementSnapshotSchema = z.object({
  party: z.object({ id, name: z.string(), phone: ntext }).nullish(),
  direction: directionSchema,
  period_from: z.string(),
  period_to: z.string(),
  opening_balance: moneySchema,
  closing_balance: moneySchema,
  lines: z.array(statementLineSchema).nullish(),
});

export const statementVersionSchema = z.object({
  statement_id: id,
  doc_no: z.string(),
  version_no: z.number().int(),
  status: statementStatusSchema,
  opening_balance: moneySchema,
  closing_balance: moneySchema,
  snapshot: statementSnapshotSchema,
  created_at: z.string(),
});
export type StatementVersion = z.infer<typeof statementVersionSchema>;

const printPartySchema = z.object({ id, name: z.string(), phone: ntext, tax_code: ntext, address: ntext });

/** `GET /print/statement/{id}`: the store header, the party and the frozen statement content. */
export const printStatementSchema = z.object({
  label: z.string(),
  title: z.string(),
  doc_no: z.string(),
  date: z.string(),
  tenant: z.object({ name: z.string(), legal_name: z.string(), tax_code: ntext, address: ntext, phone: ntext }).nullish(),
  customer: printPartySchema.nullish(),
  supplier: printPartySchema.nullish(),
  version: z.number().int().nullish(),
  sent_at: nid,
  printed_at: z.string(),
  statement: statementSnapshotSchema.omit({ party: true }),
});
export type PrintStatement = z.infer<typeof printStatementSchema>;

/* ---------- lookups ---------- */

export const fundSchema = z.object({ id, kind: z.enum(["cash", "bank"]), name: z.string(), balance: moneySchema, active: z.boolean() });
export type Fund = z.infer<typeof fundSchema>;

export const paymentMethodSchema = z.object({ id, code: z.string(), name: z.string(), fund_account_id: id, active: z.boolean() });
export type PaymentMethod = z.infer<typeof paymentMethodSchema>;

export const reasonSchema = z.object({ id, kind: z.string(), name: z.string(), active: z.boolean() });
export type Reason = z.infer<typeof reasonSchema>;

/** A customer's debt profile: group, credit limit and terms (`GET /customers/{id}`). */
export const customerProfileSchema = z.object({
  id,
  name: z.string(),
  customer_group_name: ntext,
  credit_limit: moneySchema.nullish(),
  payment_terms_days: z.number().int().nullish(),
  is_supplier: z.boolean().nullish(),
});
export type CustomerProfile = z.infer<typeof customerProfileSchema>;

/** A supplier's terms (`GET /suppliers/{id}`). */
export const supplierProfileSchema = z.object({ id, payment_terms_days: z.number().int().nullish(), is_customer: z.boolean().nullish() });
export type SupplierProfile = z.infer<typeof supplierProfileSchema>;

/** A row of the customer or supplier directory, for parties without a balance ("Tất cả khách"). */
export const directoryPartySchema = z.object({
  id,
  name: z.string(),
  phone: ntext,
  customer_group_name: ntext,
  credit_limit: moneySchema.nullish(),
});
export type DirectoryParty = z.infer<typeof directoryPartySchema>;

export const consentsSchema = z.object({
  current: z.array(z.object({ channel: z.string(), purpose: z.string(), granted: z.boolean() })),
});

/* ---------- debt adjustments and offsets ---------- */

export const adjustmentKindSchema = z.enum(["increase", "decrease", "write_off", "opening_balance", "dispute_settlement"]) satisfies z.ZodType<S["DebtAdjustment"]["kind"]>;
export type AdjustmentKind = z.infer<typeof adjustmentKindSchema>;

/** One row of `GET /debt-adjustments` (a DN): party, reason and the document a reducing kind settles. */
export const debtAdjustmentRowSchema = z.object({
  id,
  doc_no: z.string(),
  party_id: id,
  direction: directionSchema,
  kind: adjustmentKindSchema,
  amount: moneySchema,
  business_date: z.string(),
  note: z.string().nullable(),
  party_name: z.string(),
  target_doc_no: z.string().nullable(),
  reason_name: z.string(),
}) satisfies z.ZodType<Pick<S["DebtAdjustmentRow"], "id" | "doc_no" | "party_id" | "direction" | "kind" | "amount" | "business_date" | "note" | "party_name" | "target_doc_no" | "reason_name">>;
export type DebtAdjustmentRow = z.infer<typeof debtAdjustmentRowSchema>;

/** `GET /debt-adjustments/{id}`: the row, the document an increase opened and the settlements. */
export const debtAdjustmentDetailSchema = debtAdjustmentRowSchema.extend({
  obligation: z.object({ id, source_doc_no: z.string() }).nullable(),
  settlements: z.array(documentSettlementSchema),
}) satisfies z.ZodType<{ obligation: Pick<S["Obligation"], "id" | "source_doc_no"> | null; settlements: Pick<S["DocumentSettlement"], "obligation_doc_no">[] }>;
export type DebtAdjustmentDetail = z.infer<typeof debtAdjustmentDetailSchema>;

/** One row of `GET /debt-offsets` (a BT): the receivable and payable documents it nets. */
export const debtOffsetRowSchema = z.object({
  id,
  doc_no: z.string(),
  party_id: id,
  amount: moneySchema,
  business_date: z.string(),
  note: z.string().nullable(),
  party_name: z.string(),
  receivable_doc_no: z.string(),
  payable_doc_no: z.string(),
}) satisfies z.ZodType<Pick<S["DebtOffsetRow"], "id" | "doc_no" | "party_id" | "amount" | "business_date" | "note" | "party_name" | "receivable_doc_no" | "payable_doc_no">>;
export type DebtOffsetRow = z.infer<typeof debtOffsetRowSchema>;

/** `GET /debt-offsets/{id}`: the row with its settlements. */
export const debtOffsetDetailSchema = debtOffsetRowSchema.extend({
  settlements: z.array(documentSettlementSchema),
}) satisfies z.ZodType<Pick<S["DebtOffsetDetail"], "settlements">>;
export type DebtOffsetDetail = z.infer<typeof debtOffsetDetailSchema>;

/* ---------- posting results ---------- */

export const postedPaymentSchema = z.object({ id, doc_no: z.string(), amount: moneySchema, unallocated: moneySchema.nullish() });
export const postedAdjustmentSchema = z.object({ id, doc_no: z.string() });
export const suggestionSchema = z.object({ allocations: z.array(z.object({ obligation_id: id, amount: moneySchema })) });
