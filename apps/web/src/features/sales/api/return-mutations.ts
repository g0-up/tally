import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { EXCEPTION_SUB, useConfirmException } from "../../../shared/hooks/use-confirm-exception";
import type { SalesLineInput } from "../components/line-items/draft";
import { exchangePostedSchema, exchangePreviewSchema, paymentResultSchema, salesReturnPostedSchema, type Exchange, type ExchangePreview, type SalesReturn } from "../schemas";
import { POSTING_KEYS, translated, withDoc, withExceptionIds, withExceptions } from "./mutations";
import { salesKeys } from "./queries";

// Write side of the returns screen. A return posts at once: stock comes back (sellable or
// quarantine), the customer's open debt shrinks first and what is left becomes a refund due. An
// exchange is a return plus a new sale in one command; its preview is asked before posting and the
// amounts the owner saw travel with the command so a change in between is refused.

/** One returned line as `POST /sales-returns` and the exchange commands take it. */
export interface ReturnLineInput {
  sales_document_line_id: string;
  unit_id: string;
  color_id?: string;
  /** In the unit of the sold line. */
  qty: string;
  restock_to: "sellable" | "quarantine";
}

/** A returned line without a source document: the owner names the SKU and the price taken back. */
export interface FreeReturnLineInput {
  sku_id: string;
  unit_id: string;
  color_id?: string;
  qty: string;
  unit_price: string;
  restock_to: "sellable" | "quarantine";
}

export type ReturnVars =
  | { salesDocumentId: string; reasonId: string; lines: ReturnLineInput[]; refundPaymentMethodId: string | null }
  | { salesDocumentId: null; customerPartyId: string | null; reasonId: string; lines: FreeReturnLineInput[]; refundPaymentMethodId: string | null };

function returnBody(v: ReturnVars) {
  const source = v.salesDocumentId !== null ? { sales_document_id: v.salesDocumentId } : v.customerPartyId ? { customer_party_id: v.customerPartyId } : {};
  return {
    ...source,
    reason_id: v.reasonId,
    lines: v.lines,
    // Fund that pays a walk-in customer back at once; a named customer gets a refund due instead.
    ...(v.refundPaymentMethodId ? { refund_payment_method_id: v.refundPaymentMethodId } : {}),
  };
}

/** newReturn "Ghi sổ trả hàng". */
export function usePostReturn(after: (posted: SalesReturn) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: ReturnVars, { idempotencyKey }) =>
      translated(() => client.post("/sales-returns", returnBody(v), { idempotencyKey, schema: salesReturnPostedSchema })),
    success: (r) => withDoc("Đã ghi sổ ", r.sales_return.doc_no, r.refund ? `, hoàn khách bằng ${r.refund.doc_no}.` : "."),
    invalidate: POSTING_KEYS,
    after: (r) => after(r.sales_return),
  });
}

/** Body of `POST /exchanges:preview`; `POST /exchanges` takes the same fields. */
export interface ExchangeBody {
  original_sales_document_id: string;
  reason_id: string;
  return_lines: ReturnLineInput[];
  lines: SalesLineInput[];
  payment_method_id?: string;
}

/**
 * What the exchange on screen will post (amounts and priced lines). The preview is a read without
 * side effects, so it is cached by its body and kept on screen while the next one loads.
 */
export function useExchangePreview(body: ExchangeBody | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: [...salesKeys.exchanges(), "preview", body],
    queryFn: ({ signal }) => client.post("/exchanges:preview", body, { schema: exchangePreviewSchema, signal, silent: true }),
    enabled: body !== null,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    retry: false,
  });
}

export interface ExchangeVars {
  body: ExchangeBody;
  /** The preview the owner saw; the server refuses with exchange_changed when it moved. */
  preview: ExchangePreview;
}

/** newExchange "Ghi sổ đổi hàng". */
export function usePostExchange(after: (posted: Exchange) => void) {
  const client = useApiClient();
  const confirm = useConfirmException();
  return useCommit({
    mutationFn: (v: ExchangeVars, { idempotencyKey }) => {
      const body = {
        ...v.body,
        expected_customer_pays_amount: v.preview.customer_pays_amount,
        expected_store_refunds_amount: v.preview.store_refunds_amount,
      };
      return withExceptions(
        (ids) => client.post("/exchanges", withExceptionIds(body, ids), { idempotencyKey, schema: exchangePostedSchema }),
        confirm,
        (kind) => ({
          kind,
          subject: { doc_type: "exchange", doc_id: v.body.original_sales_document_id, doc_version: 0 },
          payload: body,
          title: "Xác nhận ngoại lệ cho phiếu đổi hàng",
          sub: EXCEPTION_SUB,
        }),
      );
    },
    success: (r) => withDoc("Đã ghi sổ ", r.exchange.doc_no, `, chứng từ bán mới ${r.sales_document.doc_no}${r.payment ? `, phiếu ${r.payment.doc_no}` : ""}.`),
    invalidate: POSTING_KEYS,
    after: (r) => after(r.exchange),
  });
}

export interface RefundVars {
  obligationId: string;
  paymentMethodId: string;
  amount: string;
}

/** Refund modal "Ghi phiếu chi": pays back the refund due a return left. */
export function usePostRefund(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: RefundVars, { idempotencyKey }) =>
      translated(() =>
        client.post("/refunds", { obligation_id: v.obligationId, payment_method_id: v.paymentMethodId, amount: v.amount }, { idempotencyKey, schema: paymentResultSchema }),
      ),
    success: (p) => withDoc("Đã ghi ", p.doc_no, "."),
    invalidate: POSTING_KEYS,
    after: () => after(),
  });
}
