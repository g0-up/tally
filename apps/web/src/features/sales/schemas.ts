import { z } from "zod";
import type { Money } from "../../shared/domain/money";
import type { Qty } from "../../shared/domain/qty";
import type { components } from "../../shared/api/schema";
import { isDecimalString } from "../../shared/lib/decimal";

type S = components["schemas"];

// Response schemas of the sales, POS and lookup endpoints these screens read (OpenAPI is the
// authority). They validate only the fields the screens use; unknown fields are dropped.

const decimal = z.string().refine(isDecimalString, "decimal string");
export const moneySchema = decimal.transform((s) => s as Money);
export const qtySchema = decimal.transform((s) => s as Qty);
const id = z.string();
const nid = z.string().nullable();

export const lineKindSchema = z.enum(["item", "mix_pack", "mix_flavor", "combo", "combo_component", "gift"]);
export type LineKind = z.infer<typeof lineKindSchema>;

/* ---------- lookups ---------- */

export const colorSchema = z.object({ id, code: z.string(), name: z.string(), sort_order: z.number(), active: z.boolean() });
export type ApiColor = z.infer<typeof colorSchema>;

export const unitSchema = z.object({ id, name: z.string(), active: z.boolean() });
export type Unit = z.infer<typeof unitSchema>;

export const reasonSchema = z.object({ id, kind: z.string(), name: z.string(), active: z.boolean() });
export type Reason = z.infer<typeof reasonSchema>;

export const paymentMethodSchema = z.object({
  id,
  code: z.string(),
  name: z.string(),
  fund_account_id: id,
  fund_kind: z.enum(["cash", "bank"]),
  requires_verification: z.boolean().optional(),
  active: z.boolean(),
});
export type PaymentMethod = z.infer<typeof paymentMethodSchema>;

export const customerSchema = z.object({
  id,
  name: z.string(),
  phone: nid,
  customer_group_id: z.string().optional(),
  customer_group_name: z.string().optional(),
  credit_limit: moneySchema.nullable(),
  payment_terms_days: z.number().int(),
  status: z.enum(["active", "inactive"]),
});
export type Customer = z.infer<typeof customerSchema>;

export const searchItemSchema = z.object({
  kind: z.string(),
  id,
  code: z.string(),
  label: z.string(),
  sublabel: nid,
  unit: nid,
  price_hint: moneySchema.nullable(),
  on_hand_by_color: z.array(z.object({ color_id: nid, qty: qtySchema })),
});
export type SearchItem = z.infer<typeof searchItemSchema>;

export const ledgerPaymentSchema = z.object({
  id,
  doc_no: z.string(),
  direction: z.enum(["in", "out"]),
  purpose: z.string(),
  amount: moneySchema,
  status: z.string(),
  earmarked_invoice_id: nid.optional(),
  business_date: z.string(),
});
export type LedgerPayment = z.infer<typeof ledgerPaymentSchema>;

export const ledgerObligationSchema = z.object({
  id,
  direction: z.enum(["receivable", "payable"]),
  source_type: z.string(),
  source_id: id,
  source_doc_no: z.string(),
  amount: moneySchema,
  settled_amount: moneySchema,
  open: moneySchema,
  status: z.enum(["open", "settled"]),
});
export type LedgerObligation = z.infer<typeof ledgerObligationSchema>;

export const debtLedgerSchema = z.object({
  party: z.object({ id, name: z.string(), phone: nid }),
  receivable: moneySchema,
  documents: z.array(ledgerObligationSchema).default([]),
  advances: z.array(ledgerPaymentSchema),
  pending_verification: z.array(ledgerPaymentSchema),
});
export type DebtLedger = z.infer<typeof debtLedgerSchema>;

export const mixGroupSchema = z.object({
  id,
  name: z.string(),
  pack_unit_id: id,
  units_per_pack: z.number().int(),
  part_unit_id: id,
  units_per_part: z.number().int(),
  piece_unit_id: id,
  status: z.enum(["active", "inactive"]),
  items: z.array(z.object({ sku_id: id, sku_code: z.string(), sku_name: z.string(), flavor_label: z.string(), active: z.boolean() })),
});
export type MixGroup = z.infer<typeof mixGroupSchema>;

export const skuUnitSchema = z.object({
  unit_id: id,
  unit_name: z.string(),
  factor: decimal,
  is_base: z.boolean(),
  active: z.boolean(),
  is_default_sale: z.boolean(),
});
export type SkuUnit = z.infer<typeof skuUnitSchema>;

export const skuDetailSchema = z.object({
  id,
  code: z.string(),
  display_name: z.string(),
  variant_label: z.string(),
  min_price: moneySchema.nullable(),
  units: z.array(skuUnitSchema),
});
export type SkuDetail = z.infer<typeof skuDetailSchema>;

export const skuStockSchema = z.object({
  sku_id: id,
  unit_name: z.string(),
  available: qtySchema,
  colors: z.array(z.object({ color_id: nid, sellable: qtySchema })),
});
export type SkuStock = z.infer<typeof skuStockSchema>;

export const stockRowSchema = z.object({
  sku_id: id,
  code: z.string(),
  name: z.string(),
  variant_label: z.string(),
  unit_name: z.string(),
  available: qtySchema,
  price: moneySchema.nullable(),
  colors: z.array(z.object({ color_id: nid, color_code: nid, sellable: qtySchema })),
});
export type StockRow = z.infer<typeof stockRowSchema>;

export const stockCardSchema = z.object({
  rows: z.array(
    z.object({
      id,
      doc_id: id,
      lot_no: z.string(),
      expiry_date: nid,
      color_id: nid,
      qty: qtySchema,
    }),
  ),
});
export type StockCardMove = z.infer<typeof stockCardSchema>["rows"][number];

/* ---------- pricing ---------- */

export const priceSourceSchema = z.object({
  kind: z.enum(["customer_list", "group_list", "season_list", "base_list", "manual"]),
  price_list_name: nid,
  color_id: nid,
});
export type PriceSource = z.infer<typeof priceSourceSchema>;

export const resolvedPriceSchema = z.object({ amount: moneySchema, unit_id: id, price_source: priceSourceSchema });
export type ResolvedPrice = z.infer<typeof resolvedPriceSchema>;

export const calculatedLineSchema = z.object({
  draft_index: z.number().int().nullable(),
  line_kind: z.enum(["item", "gift"]),
  sku_id: id,
  color_id: nid,
  unit_id: id,
  qty: qtySchema,
  unit_price: moneySchema,
  gross_amount: moneySchema,
  discount_amount: moneySchema,
  line_amount: moneySchema,
  below_min_price: z.boolean(),
  price_source: priceSourceSchema,
});
export type CalculatedLine = z.infer<typeof calculatedLineSchema>;

export const calculatedDocumentSchema = z.object({
  lines: z.array(calculatedLineSchema),
  goods_amount: moneySchema,
  discount_amount: moneySchema,
  tax_amount: moneySchema,
  total_amount: moneySchema,
});
export type CalculatedDocument = z.infer<typeof calculatedDocumentSchema>;

/* ---------- documents ---------- */

const salesLineShape = {
  id,
  line_no: z.number().int(),
  sku_id: nid,
  sku_code: nid,
  name: z.string(),
  variant_label: z.string(),
  unit_id: id,
  unit_name: z.string(),
  unit_factor: decimal,
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  spec_text: nid,
  line_kind: lineKindSchema,
  parent_line_id: nid,
  mix_key: nid,
  mix_group_id: nid,
  unit_price: moneySchema,
  discount_amount: moneySchema,
  line_amount: moneySchema,
  min_price: moneySchema.nullable(),
  price_source: z.unknown(),
};
export const salesLineSchema = z.object(salesLineShape);
export type SalesLine = z.infer<typeof salesLineSchema>;

const totalsShape = {
  goods_amount: moneySchema,
  discount_amount: moneySchema,
  tax_amount: moneySchema,
  total_amount: moneySchema,
};

export const quoteStatusSchema = z.enum(["draft", "sent", "accepted", "rejected", "converted", "expired"]);
export type QuoteStatus = z.infer<typeof quoteStatusSchema>;

export const quoteSchema = z.object({
  id,
  doc_no: z.string(),
  customer_party_id: id,
  quote_date: z.string(),
  valid_until: z.string(),
  status: quoteStatusSchema,
  note: nid,
  converted_invoice_id: nid,
  copied_from_id: nid,
  revision_no: z.number().int(),
  version: z.number().int(),
  ...totalsShape,
  lines: z.array(z.object({ ...salesLineShape, retail_price: moneySchema.nullable() })).optional(),
});
export type Quote = z.infer<typeof quoteSchema>;
export type QuoteLine = NonNullable<Quote["lines"]>[number];

export const quoteHistorySchema = z.object({
  data: z.array(z.object({ id, action: z.string(), actor_type: z.string(), created_at: z.string(), after: z.unknown() })),
});
export type QuoteHistoryEntry = z.infer<typeof quoteHistorySchema>["data"][number];

export const paymentStateSchema = z.object({
  total_amount: moneySchema,
  paid_amount: moneySchema,
  open_amount: moneySchema,
  pending_amount: moneySchema,
  code: z.enum(["unpaid", "partial", "paid", "overpaid"]),
  label: z.string(),
}) satisfies z.ZodType<S["SalesPaymentState"]>;
export type PaymentState = z.infer<typeof paymentStateSchema>;

export const invoiceStatusSchema = z.enum(["draft", "confirmed", "partially_delivered", "completed", "cancelled_remaining"]);
export type InvoiceStatus = z.infer<typeof invoiceStatusSchema>;

export const invoiceLineSchema = z.object({
  ...salesLineShape,
  reserved_base_qty: qtySchema,
  backorder_base_qty: qtySchema,
  delivered_base_qty: qtySchema,
  cancelled_base_qty: qtySchema,
});
export type InvoiceLine = z.infer<typeof invoiceLineSchema>;

export const invoiceSchema = z.object({
  id,
  doc_no: z.string(),
  customer_party_id: id,
  payer_party_id: id,
  invoice_date: z.string(),
  due_date: nid,
  status: invoiceStatusSchema,
  delivered_amount: moneySchema,
  quote_id: nid,
  copied_from_id: nid,
  note: nid,
  current_version: z.number().int(),
  created_at: z.string(),
  version: z.number().int(),
  ...totalsShape,
  lines: z.array(invoiceLineSchema).optional(),
  payment: paymentStateSchema.exactOptional(),
  /** What the customer has paid, flat on a list row. */
  paid_amount: moneySchema.exactOptional(),
}) satisfies z.ZodType<Pick<S["Invoice"], "id" | "doc_no" | "status" | "delivered_amount" | "total_amount" | "payment" | "paid_amount">>;
export type Invoice = z.infer<typeof invoiceSchema>;

export const invoiceCheckSchema = z.object({
  color_required_line_nos: z.array(z.number().int()),
  below_min_price_line_nos: z.array(z.number().int()),
  credit_limit_exceeded: z.boolean(),
  exceptions_required: z.array(z.string()),
  lines: z.array(
    z.object({ line_id: id, line_no: z.number().int(), remaining_base_qty: qtySchema, reservable_base_qty: qtySchema, backorder_base_qty: qtySchema }),
  ),
  ready: z.boolean(),
});
export type InvoiceCheck = z.infer<typeof invoiceCheckSchema>;

export const invoiceVersionsSchema = z.object({
  data: z.array(z.object({ id, version_no: z.number().int(), change_summary: nid, created_at: z.string() })),
});
export type InvoiceVersion = z.infer<typeof invoiceVersionsSchema>["data"][number];

export const salesDocumentLineSchema = z.object({
  ...salesLineShape,
  invoice_line_id: nid,
  returned_base_qty: qtySchema,
});
export type SalesDocumentLine = z.infer<typeof salesDocumentLineSchema>;

export const salesDocumentSourceSchema = z.enum(["stock_out", "pos", "pos_offline", "exchange"]);

export const salesDocumentSchema = z.object({
  id,
  doc_no: z.string(),
  source: salesDocumentSourceSchema,
  stock_out_id: nid,
  invoice_id: nid,
  pos_cart_id: nid,
  customer_party_id: nid,
  payer_party_id: nid,
  business_date: z.string(),
  status: z.enum(["posted", "reversed", "partially_reversed"]),
  posted_at: z.string(),
  cash_tendered_amount: moneySchema,
  change_amount: moneySchema,
  ...totalsShape,
  lines: z.array(salesDocumentLineSchema).optional(),
});
export type SalesDocument = z.infer<typeof salesDocumentSchema>;

export const salesDocumentDetailSchema = salesDocumentSchema.extend({ payment: paymentStateSchema });
export type SalesDocumentDetail = z.infer<typeof salesDocumentDetailSchema>;

export const stockOutSchema = z.object({
  id,
  doc_no: z.string(),
  invoice_id: id,
  business_date: z.string(),
  note: nid,
  status: z.enum(["posted", "reversed"]),
  lines: z.array(z.object({ id, invoice_line_id: id, sku_id: id, color_id: nid, base_qty: qtySchema })).optional(),
  sales_document_id: nid,
  sales_document: salesDocumentDetailSchema.nullable(),
});
export type StockOut = z.infer<typeof stockOutSchema>;

export const returnableLineSchema = z.object({
  sales_document_line_id: id,
  line_no: z.number().int(),
  line_kind: lineKindSchema,
  parent_line_id: nid,
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  variant_label: z.string(),
  color_id: nid,
  unit_id: id,
  unit_name: z.string(),
  unit_factor: decimal,
  sold_base_qty: qtySchema,
  returned_base_qty: qtySchema,
  returnable_base_qty: qtySchema,
  line_amount: moneySchema,
  unit_amount: moneySchema,
});
export type ReturnableLine = z.infer<typeof returnableLineSchema>;

export const salesReturnLineSchema = z.object({
  id,
  sales_document_line_id: nid,
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  variant_label: z.string(),
  unit_name: z.string(),
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  unit_price: moneySchema,
  line_amount: moneySchema,
  restock_to: z.enum(["sellable", "quarantine"]),
});
export type SalesReturnLine = z.infer<typeof salesReturnLineSchema>;

export const salesReturnSchema = z.object({
  id,
  doc_no: z.string(),
  customer_party_id: nid,
  sales_document_id: nid,
  reason_id: id,
  business_date: z.string(),
  total_amount: moneySchema,
  debt_reduction_amount: moneySchema,
  refund_due_amount: moneySchema,
  exchange_id: nid,
  status: z.enum(["posted", "reversed"]),
  lines: z.array(salesReturnLineSchema).optional(),
});
export type SalesReturn = z.infer<typeof salesReturnSchema>;

export const salesPaymentSchema = z.object({ id, doc_no: z.string(), status: z.string(), amount: moneySchema });
export type SalesPayment = z.infer<typeof salesPaymentSchema>;

export const salesReturnPostedSchema = z.object({ sales_return: salesReturnSchema, refund: salesPaymentSchema.nullable() });

export const exchangeSchema = z.object({
  id,
  doc_no: z.string(),
  customer_party_id: nid,
  original_sales_document_id: id,
  sales_return_id: id,
  new_sales_document_id: id,
  returned_amount: moneySchema,
  taken_amount: moneySchema,
  debt_reduction_amount: moneySchema,
  customer_pays_amount: moneySchema,
  store_refunds_amount: moneySchema,
  business_date: z.string(),
});
export type Exchange = z.infer<typeof exchangeSchema>;

export const exchangePreviewSchema = z.object({
  returned_amount: moneySchema,
  taken_amount: moneySchema,
  debt_reduction_amount: moneySchema,
  customer_pays_amount: moneySchema,
  store_refunds_amount: moneySchema,
  message: z.string(),
  return_lines: z.array(salesReturnLineSchema).default([]),
  lines: z.array(salesLineSchema),
});
export type ExchangePreview = z.infer<typeof exchangePreviewSchema>;

export const exchangePostedSchema = z.object({
  exchange: exchangeSchema,
  sales_return: salesReturnSchema,
  sales_document: salesDocumentDetailSchema,
  payment: salesPaymentSchema.nullable(),
});
export type ExchangePosted = z.infer<typeof exchangePostedSchema>;

export const paymentResultSchema = z.object({ id, doc_no: z.string(), amount: moneySchema, status: z.string() });
export type PaymentResult = z.infer<typeof paymentResultSchema>;

/* ---------- POS ---------- */

export const shiftSchema = z.object({ id, doc_no: z.string(), business_date: z.string(), opened_at: z.string(), closed_at: nid, status: z.enum(["open", "closed"]) });
export type Shift = z.infer<typeof shiftSchema>;

export const posPaymentMethodSchema = z.object({
  id,
  code: z.string(),
  name: z.string(),
  fund_account_id: id,
  fund_kind: z.enum(["cash", "bank"]),
  cash: z.boolean(),
  active: z.boolean(),
});
export type PosPaymentMethod = z.infer<typeof posPaymentMethodSchema>;

export const posTileSchema = z.object({
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  variant_label: z.string(),
  unit_id: id,
  unit_name: z.string(),
  retail_price: moneySchema.nullable(),
  stock: z.array(z.object({ color_id: nid, qty: qtySchema })),
  // A device bundle saved before tiles carried barcodes has none; readers still use `?? []`,
  // because a saved bundle is read back without this schema.
  barcodes: z.array(z.object({ code: z.string(), unit_id: id, unit_name: z.string() })).default([]),
});
export type PosTile = z.infer<typeof posTileSchema>;

export const posBootstrapSchema = z.object({
  payment_methods: z.array(posPaymentMethodSchema),
  shift: shiftSchema.nullable(),
  tiles: z.array(posTileSchema),
  priced_at: z.string(),
});
export type PosBootstrap = z.infer<typeof posBootstrapSchema>;

export const posCartSchema = z.object({
  id,
  doc_no: z.string(),
  customer_party_id: nid,
  label: nid,
  status: z.enum(["open", "sold"]),
  total_amount: moneySchema,
  sales_document_id: nid,
  copied_from_id: nid,
  created_at: z.string(),
  updated_at: z.string(),
  version: z.number().int(),
  lines: z.array(salesLineSchema).optional(),
});
export type PosCart = z.infer<typeof posCartSchema>;

export const posSaleSchema = z.object({ sales_document: salesDocumentDetailSchema, shift: shiftSchema, payments: z.array(salesPaymentSchema) });
export type PosSale = z.infer<typeof posSaleSchema>;

/* ---------- print ---------- */

export const printDocTypeSchema = z.enum(["quote", "invoice", "stock_out", "sales_document", "sales_return", "exchange"]);
export type PrintDocType = z.infer<typeof printDocTypeSchema>;

const printLineSchema = z.object({
  line_no: z.number().int(),
  line_kind: lineKindSchema,
  sku_code: nid,
  name: z.string(),
  variant_label: z.string(),
  color: nid,
  spec_text: nid,
  unit_name: z.string(),
  qty: qtySchema,
  unit_price: moneySchema,
  discount_amount: moneySchema,
  line_amount: moneySchema,
});
export type PrintLine = z.infer<typeof printLineSchema>;

const printPartySchema = z.object({ name: z.string(), phone: nid, tax_code: nid, address: nid });

export const printDocSchema = z.object({
  label: z.string(),
  doc_type: printDocTypeSchema,
  title: z.string(),
  id,
  doc_no: z.string(),
  date: z.string(),
  status: z.string(),
  tenant: printPartySchema.extend({ legal_name: z.string() }).nullable(),
  customer: printPartySchema.nullable(),
  note: nid,
  groups: z.array(z.object({ mix_key: nid, lines: z.array(printLineSchema) })),
  returned_lines: z.array(printLineSchema).optional(),
  totals: z.object(totalsShape),
  payment: paymentStateSchema.nullable(),
  amounts: z.array(z.object({ code: z.string(), label: z.string(), amount: moneySchema })),
  related: z.array(z.object({ doc_type: printDocTypeSchema, id, doc_no: z.string() })),
  printed_at: z.string(),
});
export type PrintDoc = z.infer<typeof printDocSchema>;

/** `{data: [...], next_cursor: null}` envelope of the small lookup lists. */
export function listOf<T extends z.ZodType>(item: T) {
  return z.object({ data: z.array(item) });
}
