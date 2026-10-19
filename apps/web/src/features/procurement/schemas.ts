import { z } from "zod";
import { decimalSchema, moneySchema, qtySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the procurement endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped.

const id = z.string();
const nid = z.string().nullish();
const ntext = z.string().nullish();

/* ---------- purchase orders ---------- */

export const orderStatusSchema = z.enum(["draft", "confirmed", "partially_received", "completed", "closed_short"]);
export type OrderStatus = z.infer<typeof orderStatusSchema>;

export const orderLineSchema = z.object({
  id,
  line_no: z.number().int(),
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  variant_label: ntext,
  unit_id: id,
  unit_name: z.string(),
  unit_factor: decimalSchema,
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  spec_text: ntext,
  unit_price: moneySchema,
  discount_amount: moneySchema.nullish(),
  line_amount: moneySchema,
  retail_price: moneySchema.nullish(),
  received_base_qty: qtySchema,
  returned_base_qty: qtySchema.nullish(),
  pending_base_qty: qtySchema,
});
export type OrderLine = z.infer<typeof orderLineSchema>;

export const orderSchema = z.object({
  id,
  doc_no: z.string(),
  supplier_party_id: id,
  supplier_name: z.string(),
  order_date: z.string(),
  expected_date: nid,
  status: orderStatusSchema,
  goods_amount: moneySchema.nullish(),
  total_amount: moneySchema,
  note: ntext,
  close_reason_id: nid,
  created_at: z.string(),
  updated_at: z.string().nullish(),
  version: z.number().int(),
  ordered_base_qty: qtySchema.nullish(),
  received_base_qty: qtySchema.nullish(),
  pending_base_qty: qtySchema.nullish(),
  days_late: z.number().int().nullish(),
  lines: z.array(orderLineSchema).nullish(),
});
export type Order = z.infer<typeof orderSchema>;

/* ---------- receipts ---------- */

export const receiptLineSchema = z.object({
  id,
  line_no: z.number().int(),
  purchase_order_line_id: nid,
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  variant_label: ntext,
  unit_id: id,
  unit_name: z.string(),
  unit_factor: decimalSchema,
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  spec_text: ntext,
  condition: z.string(),
  lot_no: ntext,
  expiry_date: nid,
  lot_id: nid,
  unit_price: moneySchema,
  line_amount: moneySchema,
  landed_cost_amount: moneySchema.nullish(),
  unit_cost: moneySchema.nullish(),
  returned_base_qty: qtySchema.nullish(),
  returnable_base_qty: qtySchema,
});
export type ReceiptLine = z.infer<typeof receiptLineSchema>;

export const payableSchema = z.object({
  id,
  amount: moneySchema,
  settled_amount: moneySchema,
  open_amount: moneySchema,
  due_date: nid,
  status: z.string(),
});
export type PayableInfo = z.infer<typeof payableSchema>;

export const attachmentSchema = z.object({
  id,
  file_name: z.string(),
  content_type: z.string(),
  size_bytes: z.number(),
  scan_status: z.string(),
  created_at: z.string(),
});
export type Attachment = z.infer<typeof attachmentSchema>;

export const receiptPaymentSchema = z.object({ id, doc_no: z.string(), amount: moneySchema, business_date: z.string(), status: z.string() });
export type ReceiptPayment = z.infer<typeof receiptPaymentSchema>;

export const landedCostSchema = z.object({
  id,
  cost_type: z.string(),
  description: ntext,
  amount: moneySchema,
  method: z.string(),
  business_date: z.string(),
});
export type LandedCost = z.infer<typeof landedCostSchema>;

export const receiptSchema = z.object({
  id,
  doc_no: z.string(),
  supplier_party_id: nid,
  supplier_name: ntext,
  purchase_order_id: nid,
  purchase_order_doc_no: nid,
  business_date: z.string(),
  goods_amount: moneySchema,
  tax_amount: moneySchema,
  landed_cost_amount: moneySchema,
  total_amount: moneySchema,
  paid_now_amount: moneySchema.nullish(),
  due_date: nid,
  status: z.string(),
  reversal_doc_no: nid,
  lines: z.array(receiptLineSchema).nullish(),
  payable: payableSchema.nullish(),
  payments: z.array(receiptPaymentSchema).nullish(),
  landed_costs: z.array(landedCostSchema).nullish(),
  attachments: z.array(attachmentSchema).nullish(),
});
export type Receipt = z.infer<typeof receiptSchema>;

export const receiptListItemSchema = receiptSchema.extend({
  supplier_document_id: nid,
  supplier_document_no: nid,
  open_amount: moneySchema.nullish(),
});
export type ReceiptListItem = z.infer<typeof receiptListItemSchema>;

/* ---------- supplier returns ---------- */

export const purchaseReturnLineSchema = z.object({
  id,
  receipt_line_id: id,
  sku_id: id,
  sku_code: z.string().nullish(),
  name: z.string(),
  variant_label: ntext,
  unit_name: z.string(),
  qty: qtySchema,
  color_id: nid,
  line_amount: moneySchema.nullish(),
  lot_no: ntext,
});

export const purchaseReturnSchema = z.object({
  id,
  doc_no: z.string(),
  supplier_party_id: nid,
  supplier_name: ntext,
  receipt_id: id,
  receipt_doc_no: nid,
  reason_id: nid,
  settlement: z.string().nullish(),
  business_date: z.string(),
  total_amount: moneySchema,
  status: z.string().nullish(),
  credited_amount: moneySchema.nullish(),
  refund_due_amount: moneySchema.nullish(),
  lines: z.array(purchaseReturnLineSchema).nullish(),
});
export type PurchaseReturn = z.infer<typeof purchaseReturnSchema>;

/* ---------- posting results ---------- */

/** What a posting answers that the success toast names. */
export const postedDocSchema = z.object({ id, doc_no: z.string() });
export type PostedDoc = z.infer<typeof postedDocSchema>;

/* ---------- lookups used by the procurement dialogs ---------- */

export const reasonSchema = z.object({ id, kind: z.string(), name: z.string(), active: z.boolean() });
export type Reason = z.infer<typeof reasonSchema>;

export const paymentMethodSchema = z.object({ id, code: z.string(), name: z.string(), active: z.boolean() });
export type PaymentMethod = z.infer<typeof paymentMethodSchema>;

export const paymentSchema = z.object({
  id,
  doc_no: z.string(),
  amount: moneySchema,
  allocated_amount: moneySchema.nullish(),
  source_type: ntext,
  source_id: nid,
  business_date: z.string().nullish(),
  status: z.string(),
});
export type Payment = z.infer<typeof paymentSchema>;

export const partyLedgerSchema = z.object({ advances: z.array(paymentSchema).nullish() });

export const supplierRefSchema = z.object({ id, name: z.string(), status: z.string() });
export type SupplierRef = z.infer<typeof supplierRefSchema>;

export const supplierDocumentSchema = z.object({ id, document_no: z.string() });

export const uploadSchema = z.object({
  attachment: z.object({ id }),
  upload: z.object({ method: z.string(), url: z.string(), headers: z.record(z.string(), z.string()).nullish() }),
});

export const attachmentUrlSchema = z.object({ url: z.string() });

/** A lot of a SKU in the store (`GET /lots`), for "Tồn lô tại kho". */
export const lotSchema = z.object({
  id,
  sku_id: id,
  lot_no: z.string(),
  color_id: nid,
  expiry_date: nid,
  is_quarantine: z.boolean(),
  qty: qtySchema,
  state: z.string(),
});
export type Lot = z.infer<typeof lotSchema>;
