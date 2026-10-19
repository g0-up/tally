import { createElement, Fragment, useRef, type ReactNode } from "react";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { moneySchema } from "../../../shared/api/decimal-schemas";
import type { HttpClient } from "../../../shared/api/http";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { createOnce, newCreateState, unsettled, useUnsettled, type CreateState } from "../../../shared/hooks/create-once";
import { PartialCommitError, runFollowUps, useCommit, type FollowUpStep } from "../../../shared/hooks/use-commit";
import { dec } from "../../../shared/lib/decimal";
import { etagOf, skuKeys } from "../../catalog";
import { supplierKeys } from "../../partners";
import { postSalePrice, pricingKeys, type PriceChangeSource, type SalePriceWrites } from "../../pricing";
import type { OrderLineRequest, ReceiptLineRequest, SellChange } from "../buy-lines";
import { orderSchema, postedDocSchema } from "../schemas";
import { PROCUREMENT_MESSAGES, withProcurementMessage } from "./adapters";
import { DEBT_KEY, procurementKeys } from "./queries";

// Write side of the Nhập hàng screens. Every posting goes through useCommit (one Idempotency-Key
// per intent); each further request of an intent sends a key derived from it. An intent made of
// several requests also remembers the document it stored and the steps it finished: a retry
// continues from there with what the form holds then. A create whose answer was lost goes out
// again with the body it had (see createOnce), since the server refuses its key with another one.

/** What a posting may change: stock and costs, orders and receipts, payables, suppliers, prices. */
const POSTING_KEYS = [skuKeys.all, procurementKeys.all, DEBT_KEY, supplierKeys.all, pricingKeys.all] as const;

const savedOrderSchema = orderSchema.pick({ id: true, doc_no: true, version: true, status: true });
type SavedOrder = z.infer<typeof savedOrderSchema>;
const postedReceiptSchema = z.object({
  id: z.string(),
  doc_no: z.string(),
  supplier_party_id: z.string().nullish(),
  total_amount: moneySchema,
  tax_amount: moneySchema.nullish(),
});

/** Toast text with the document number in bold: `before <b>docNo</b> after`. */
const withDoc = (before: string, docNo: string, after: string): ReactNode => createElement(Fragment, null, before, createElement("b", null, docNo), after);

/** Runs a request and maps a refusal onto the prototype sentence. */
async function procurementCall<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (e) {
    throw withProcurementMessage(e);
  }
}

/**
 * "Giá bán lẻ" typed on document rows (prototype `setPrice(sku, sell, min(whole, sell))`), one step
 * per SKU: the wholesale price follows a retail price set below it, so it is written first.
 */
function sellPriceSteps(client: HttpClient, intentKey: string, changes: readonly SellChange[], source: PriceChangeSource, writes: SalePriceWrites): FollowUpStep[] {
  return changes.map((c) => ({
    id: `price:${c.sku.id}`,
    label: "cập nhật giá bán",
    run: async () => {
      if (c.sku.wholesale_price !== null && dec(c.retail).lt(c.sku.wholesale_price)) {
        await postSalePrice(client, intentKey, c.sku.id, { kind: "wholesale", price: c.retail, apply_all_colors: false, source }, writes);
      }
      await postSalePrice(client, intentKey, c.sku.id, { kind: "retail", price: c.retail, apply_all_colors: false, source }, writes);
    },
  }));
}

/** What an intent already stored: the document, the follow-up steps it finished and its price writes. */
interface Progress<T> {
  key: string;
  doc: T;
  done: Set<string>;
  writes: SalePriceWrites;
}

/** The toast of a document stored without every follow-up step. */
const partlyStored = (docNo: string) => (missing: string, reason: string) =>
  withDoc("Đã ghi ", docNo, ` nhưng chưa ${missing}: ${reason} Bấm lại để làm tiếp phần còn thiếu.`);

/**
 * Runs the price steps of a stored document. Each attempt runs all of them: the writes the intent
 * made decide what is left, so a price edited since (even back to an earlier value) is written.
 */
function runPriceSteps(steps: readonly FollowUpStep[], docNo: string): Promise<void> {
  return runFollowUps(steps, new Set(), partlyStored(docNo), withProcurementMessage);
}

/* ---------- purchase orders ---------- */

interface OrderBody {
  supplier_party_id: string;
  expected_date?: string;
  lines: OrderLineRequest[];
}

export interface SaveOrderVars {
  supplierId: string;
  expectedDate: string | null;
  lines: OrderLineRequest[];
  sells: SellChange[];
  /** "Lưu & chốt đơn" rather than "Lưu nháp". */
  confirm: boolean;
}

/**
 * newPO: saves the draft (`POST /purchase-orders`), then confirms it unless saved as a draft, then
 * writes the sale prices. Once the draft is stored, a retry of the intent only runs what is left.
 */
export function useSaveOrder(after: () => void) {
  const client = useApiClient();
  const progress = useRef<Progress<SavedOrder> | null>(null);
  const creating = useRef<{ key: string; state: CreateState<OrderBody, SavedOrder> } | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: SaveOrderVars, { idempotencyKey }) => {
      let p = progress.current?.key === idempotencyKey ? progress.current : null;
      if (p === null) {
        if (creating.current?.key !== idempotencyKey) creating.current = { key: idempotencyKey, state: newCreateState() };
        const create = creating.current.state;
        const body: OrderBody = { supplier_party_id: v.supplierId, ...(v.expectedDate ? { expected_date: v.expectedDate } : {}), lines: v.lines };
        const made = await lost.watch(create, () => procurementCall(() => createOnce(create, body, { send: (b) => client.post("/purchase-orders", b, { idempotencyKey, schema: savedOrderSchema }) })));
        // Stored from an earlier attempt's body: neither confirm nor price what the owner did not see.
        if (made.edited) throw new PartialCommitError(PROCUREMENT_MESSAGES.orderCreatedAsSent(made.result.doc_no));
        p = progress.current = { key: idempotencyKey, doc: made.result, done: new Set(), writes: new Map() };
      }
      const stored = p;
      const confirm: FollowUpStep = {
        id: "confirm",
        label: "chốt đơn",
        run: async () => {
          stored.doc = await client.post(`/purchase-orders/${stored.doc.id}:confirm`, {}, { idempotencyKey: `${idempotencyKey}-confirm`, ifMatch: etagOf(stored.doc.version), schema: savedOrderSchema });
        },
      };
      await runFollowUps(v.confirm ? [confirm] : [], stored.done, partlyStored(stored.doc.doc_no), withProcurementMessage);
      await runPriceSteps(sellPriceSteps(client, idempotencyKey, v.sells, "purchase_order", stored.writes), stored.doc.doc_no);
      return stored.doc;
    },
    success: (po, v) => (v.confirm ? withDoc("", po.doc_no, ": Đã chốt.") : withDoc("Đã lưu nháp ", po.doc_no, ".")),
    invalidate: POSTING_KEYS,
    after: () => {
      progress.current = null;
      after();
    },
  });
  return { ...commit, unsettled: lost.unsettled };
}

export interface OrderRefVars {
  id: string;
  docNo: string;
  version: number;
}

/** "Chốt đơn" on a draft (`:confirm`). */
export function useConfirmOrder() {
  const client = useApiClient();
  return useCommit({
    mutationFn: (o: OrderRefVars, { idempotencyKey }) =>
      procurementCall(() => client.post(`/purchase-orders/${o.id}:confirm`, {}, { idempotencyKey, ifMatch: etagOf(o.version), schema: savedOrderSchema })),
    success: (po) => withDoc("", po.doc_no, ": Đã chốt."),
    invalidate: [procurementKeys.all],
  });
}

/** poClose: closes what is still pending (`:close-short`). */
export function useCloseShort(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: OrderRefVars & { reasonId: string }, { idempotencyKey }) =>
      procurementCall(() => client.post(`/purchase-orders/${v.id}:close-short`, { reason_id: v.reasonId }, { idempotencyKey, ifMatch: etagOf(v.version), schema: savedOrderSchema })),
    success: () => "Đã đóng phần còn thiếu.",
    invalidate: [procurementKeys.all, skuKeys.all],
    after,
  });
}

export interface AdvanceVars {
  orderId: string;
  supplierId: string;
  amount: Money;
  paymentMethodId: string;
}

/** poAdvance: a PC paid to the supplier before the goods arrive, earmarked for the order. */
export function useOrderAdvance(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: AdvanceVars, { idempotencyKey }) =>
      procurementCall(() =>
        client.post(
          "/payments",
          { direction: "out", party_id: v.supplierId, purpose: "advance", payment_method_id: v.paymentMethodId, amount: v.amount, source_type: "purchase", source_id: v.orderId },
          { idempotencyKey, schema: postedDocSchema },
        ),
      ),
    success: (p) => withDoc("Đã ghi ", p.doc_no, "."),
    invalidate: [procurementKeys.all, DEBT_KEY, supplierKeys.all],
    after,
  });
}

/* ---------- receipts ---------- */

const INVOICE_STEP = "gắn hóa đơn NCC";

export interface PostReceiptVars {
  orderId: string | null;
  supplierId: string | null;
  lines: ReceiptLineRequest[];
  sells: SellChange[];
  /** "Số hóa đơn NCC (nếu có)". */
  invoiceNo: string;
  /** "Chi phí vận chuyển/bốc xếp", whole dong. */
  freight: Money | null;
}

interface SupplierDocumentBody {
  supplier_party_id: string;
  document_no: string;
  goods_amount: string;
  tax_amount: string;
  receipt_ids: string[];
}

/** Invoice body matching one receipt: its value without tax and its tax. */
function supplierDocumentBody(supplierId: string, documentNo: string, receipt: { id: string; total_amount: Money; tax_amount?: Money | null | undefined }): SupplierDocumentBody {
  const tax = receipt.tax_amount ?? "0";
  return {
    supplier_party_id: supplierId,
    document_no: documentNo,
    goods_amount: dec(receipt.total_amount).minus(tax).toFixed(),
    tax_amount: dec(tax).toFixed(),
    receipt_ids: [receipt.id],
  };
}

/**
 * newRCV "Ghi sổ nhập kho": posts the PN, then in the same intent the freight as a landed cost
 * allocated by value, the supplier invoice number and the retail prices typed on the rows. Once the
 * PN is posted its lines are settled; a retry only runs the steps still missing, with what the form
 * holds then (a corrected invoice number, for one).
 */
export function usePostReceipt(after: () => void) {
  const client = useApiClient();
  const progress = useRef<(Progress<z.infer<typeof postedReceiptSchema>> & { invoice: CreateState<SupplierDocumentBody, unknown> }) | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: PostReceiptVars, { idempotencyKey }) => {
      let p = progress.current?.key === idempotencyKey ? progress.current : null;
      if (p === null) {
        const body = {
          ...(v.orderId ? { purchase_order_id: v.orderId } : { supplier_party_id: v.supplierId }),
          paid_now_amount: "0",
          lines: v.lines,
        };
        const r = await procurementCall(() => client.post("/receipts", body, { idempotencyKey, schema: postedReceiptSchema }));
        p = progress.current = { key: idempotencyKey, doc: r, done: new Set(), writes: new Map(), invoice: newCreateState() };
      }
      const stored = p;
      const r = p.doc;
      const steps: FollowUpStep[] = [];
      if (v.freight !== null && dec(v.freight).gt(0)) {
        const amount = v.freight;
        steps.push({
          id: "landed",
          label: "ghi chi phí vận chuyển",
          run: () =>
            client.post(`/receipts/${r.id}/landed-costs`, { cost_type: "shipping", amount, method: "by_value", paid_now_amount: "0" }, { idempotencyKey: `${idempotencyKey}-landed` }),
        });
      }
      const supplierId = r.supplier_party_id ?? v.supplierId;
      const invoiceNo = v.invoiceNo.trim();
      const invoice = stored.invoice;
      // An invoice whose answer was lost goes out again whatever the form holds now (even no
      // number): only its answer says whether the receipt is matched.
      const invoiceBody = invoiceNo !== "" && supplierId ? supplierDocumentBody(supplierId, invoiceNo, r) : unsettled(invoice) ? invoice.sent : null;
      if (invoiceBody !== null) {
        steps.push({
          id: "invoice",
          label: INVOICE_STEP,
          run: () => lost.watch(invoice, () => createOnce(invoice, invoiceBody, { send: (b) => client.post<unknown>("/supplier-documents", b, { idempotencyKey: `${idempotencyKey}:invoice` }) })),
        });
      }
      const told = (missing: string, reason: string): ReactNode =>
        missing === INVOICE_STEP && unsettled(invoice) ? withDoc("Đã ghi ", r.doc_no, ` nhưng ${PROCUREMENT_MESSAGES.invoiceOutcomeUnknown}`) : partlyStored(r.doc_no)(missing, reason);
      await runFollowUps(steps, stored.done, told, withProcurementMessage);
      await runPriceSteps(sellPriceSteps(client, idempotencyKey, v.sells, v.orderId ? "purchase_order" : "product_list", stored.writes), r.doc_no);
      // The invoice went in under the number an earlier attempt sent: said once, then the intent ends.
      const matched = invoice.result !== null ? invoice.sent : null;
      if (matched !== null && matched.document_no !== invoiceNo && !stored.done.has("invoice-told")) {
        stored.done.add("invoice-told");
        throw new PartialCommitError(withDoc("Đã ghi ", r.doc_no, `. ${PROCUREMENT_MESSAGES.invoiceMatchedAsSent(matched.document_no)}`));
      }
      return r;
    },
    success: (r) => withDoc("Đã ghi ", r.doc_no, ". Tồn, nợ NCC và giá bán đã cập nhật."),
    invalidate: POSTING_KEYS,
    after: () => {
      progress.current = null;
      after();
    },
  });
  return { ...commit, unsettled: lost.unsettled };
}

export interface MatchInvoiceVars {
  supplierId: string;
  receipt: { id: string; total_amount: Money; tax_amount?: Money | null | undefined };
  documentNo: string;
}

/** rcvInvoice "Lưu đối chiếu": records the supplier invoice against one receipt. */
export function useMatchInvoice(after: () => void) {
  const client = useApiClient();
  const creating = useRef<{ key: string; state: CreateState<SupplierDocumentBody, unknown> } | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: MatchInvoiceVars, { idempotencyKey }) => {
      if (creating.current?.key !== idempotencyKey) creating.current = { key: idempotencyKey, state: newCreateState() };
      const create = creating.current.state;
      const body = supplierDocumentBody(v.supplierId, v.documentNo, v.receipt);
      const made = await lost.watch(create, () => procurementCall(() => createOnce(create, body, { send: (b) => client.post<unknown>("/supplier-documents", b, { idempotencyKey }) })));
      if (made.edited) throw new PartialCommitError(PROCUREMENT_MESSAGES.invoiceMatchedAsSent(create.sent?.document_no ?? v.documentNo));
      return made.result;
    },
    success: () => "Đã đối chiếu hóa đơn.",
    invalidate: [procurementKeys.all, DEBT_KEY],
    after,
  });
  return { ...commit, unsettled: lost.unsettled };
}

/* ---------- supplier returns ---------- */

export interface PostReturnVars {
  receiptId: string;
  reasonId: string;
  /** Quantities in the SKU's base unit (`unit_id`); without it, in the receipt line's unit. */
  lines: { receipt_line_id: string; unit_id?: string; qty: Qty }[];
}

/** newPRT "Ghi sổ xuất trả": goods back out of their lot, the payable reduced (credit_payable). */
export function usePostReturn(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: PostReturnVars, { idempotencyKey }) =>
      procurementCall(() =>
        client.post("/purchase-returns", { receipt_id: v.receiptId, reason_id: v.reasonId, settlement: "credit_payable", lines: v.lines }, { idempotencyKey, schema: postedDocSchema }),
      ),
    success: () => "Đã ghi phiếu trả NCC.",
    invalidate: POSTING_KEYS,
    after,
  });
}
