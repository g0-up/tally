import { useCallback, useRef, useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage, isApiError } from "../../../shared/api/api-error";
import { useTenant } from "../../../shared/api/platform-queries";
import { toMoney } from "../../../shared/domain/money";
import { useCommit } from "../../../shared/hooks/use-commit";
import { EXCEPTION_SUB, useConfirmException, type ExceptionPrompt } from "../../../shared/hooks/use-confirm-exception";
import { vnd } from "../../../shared/lib/format";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { toast } from "../../../shared/ui/toast-store";
import { POSTING_KEYS, etagOf, translateSalesError, translated, withDoc, withExceptionIds, withExceptions } from "../../sales/api/mutations";
import type { SalesLineInput } from "../../sales/components/line-items/draft";
import { posCartSchema, posSaleSchema, type PosCart, type PosSale } from "../../sales/schemas";
import { forgetCheckoutIntent, isUncertainOutcome, rememberCheckoutIntent } from "../offline/checkout-intents";
import type { CheckoutPayments } from "./adapters";
import { posKeys } from "./queries";

// Write side of the POS screen. A sale is one posting command: an unsaved cart goes to
// `POST /pos/checkout`; a saved cart is first saved with what is on screen, then sold with
// `POST /pos/carts/{id}:checkout`. Both carry one Idempotency-Key per checkout intent, kept by every
// retry (shift_changed / sale_changed, exception confirmations) and dropped after the 2xx.
// A checkout that got no answer may have posted: it is kept on the device with its key and body,
// the cart cannot be sold offline meanwhile, and the sync sends it again (same key) once the API
// answers, so the sale is posted once whatever happened to the first answer.

/** The saved cart being edited: its id, number and the version the screen loaded. */
export interface CartRef {
  id: string;
  docNo: string;
  version: number;
}

/** What a checkout sells, read when the command starts. */
export interface CheckoutDraft {
  customerId: string | null;
  lines: SalesLineInput[];
  payments: CheckoutPayments;
}

export interface CheckoutVars {
  cart: CartRef | null;
  /**
   * Builds the sale from the screen once the server priced the cart on it; throws the message to
   * show when the cart cannot be sold as it is. Called inside the command, so the button is already
   * busy and a second click is ignored while it waits.
   */
  draft: () => Promise<CheckoutDraft>;
}

function cartBody(customerId: string | null, lines: SalesLineInput[]) {
  return customerId ? { customer_party_id: customerId, lines } : { lines };
}

function salePrompt(kind: string, subject: ExceptionPrompt["subject"], payload: unknown, label: string): ExceptionPrompt {
  return { kind, subject, payload, title: `Xác nhận ngoại lệ cho ${label}`, sub: EXCEPTION_SUB };
}

/** "Thanh toán · F9". */
export function useCheckout(after: (sale: PosSale, vars: CheckoutVars) => void) {
  const client = useApiClient();
  const tenant = useTenant();
  const confirm = useConfirmException();
  // Version of the saved cart after this screen saved it, so a second attempt sends a fresh If-Match,
  // and the intent whose `:checkout` was sent, so a retry after a lost answer replays the sale.
  const saved = useRef<{ id: string; version: number; checkoutKey: string | null } | null>(null);
  // Key of the checkout of the cart on screen that got no answer.
  const [uncertainKey, setUncertainKey] = useState<string | null>(null);

  /** Sends one checkout request, keeping it on the device when its answer is lost. */
  const send = async (path: string, body: CheckoutPayments & object, idempotencyKey: string, ifMatch: string | null) => {
    try {
      const sale = await client.post(path, body, { idempotencyKey, ifMatch, schema: posSaleSchema });
      // Posted (or replayed): nothing left to send again. Should the device fail to drop it, the
      // sync replays it and the server answers with this same sale.
      await forgetCheckoutIntent(idempotencyKey).catch(() => undefined);
      return sale;
    } catch (e) {
      const tenantId = tenant.data?.id;
      if (isUncertainOutcome(e) && tenantId) {
        setUncertainKey(idempotencyKey);
        await rememberCheckoutIntent({
          idempotency_key: idempotencyKey,
          tenant_id: tenantId,
          path,
          body,
          if_match: ifMatch,
          total: toMoney(sumDec(body.payments.map((p) => p.amount))),
          created_at: new Date().toISOString(),
        }).catch((err: unknown) => toast.error(`Không lưu được giao dịch chưa rõ kết quả trên máy: ${errorMessage(err)} Đừng bán lại giỏ này khi chưa kiểm tra danh sách hóa đơn.`));
      }
      throw e;
    }
  };

  const commit = useCommit({
    mutationFn: async (v: CheckoutVars, { idempotencyKey }) => {
      const d = await v.draft();
      if (v.cart === null) {
        const body = { ...cartBody(d.customerId, d.lines), ...d.payments };
        const tenantId = tenant.data?.id ?? "";
        return withExceptions(
          (ids) => send("/pos/checkout", withExceptionIds(body, ids), idempotencyKey, null),
          confirm,
          (kind) => salePrompt(kind, { doc_type: "pos_checkout", doc_id: tenantId, doc_version: 0 }, body, "giỏ hàng mới"),
        );
      }
      const cart = v.cart;
      const earlier = saved.current?.id === cart.id ? saved.current : null;
      let version = earlier?.version ?? cart.version;
      try {
        const updated = await client.patch(`/pos/carts/${cart.id}`, cartBody(d.customerId, d.lines), { ifMatch: etagOf(version), schema: posCartSchema });
        version = updated.version;
        saved.current = { id: cart.id, version, checkoutKey: null };
      } catch (e) {
        // Sold by this very intent, its answer lost on the way: the checkout below replays it.
        const soldHere = isApiError(e) && e.code === "cart_sold" && earlier?.checkoutKey === idempotencyKey;
        if (!soldHere) throw translateSalesError(e);
      }
      saved.current = { id: cart.id, version, checkoutKey: idempotencyKey };
      const body = { ...d.payments };
      return withExceptions(
        (ids) => send(`/pos/carts/${cart.id}:checkout`, withExceptionIds(body, ids), idempotencyKey, etagOf(version)),
        confirm,
        (kind) => salePrompt(kind, { doc_type: "pos_cart", doc_id: cart.id, doc_version: version }, body, cart.docNo),
      );
    },
    success: (sale, v) => {
      const doc = sale.sales_document;
      const change = dec(doc.change_amount).gt(0) ? ` · trả lại ${vnd(doc.change_amount)}` : "";
      return withDoc("Đã ghi sổ ", doc.doc_no, ` · ${vnd(doc.total_amount)}${change}.${v.cart ? ` Giỏ ${v.cart.docNo} đã khóa.` : ""}`);
    },
    invalidate: POSTING_KEYS,
    after: (sale, v) => {
      saved.current = null;
      setUncertainKey(null);
      after(sale, v);
    },
  });
  const { resetKey } = commit;
  /** A new cart: its checkout is a new intent, and the earlier one is left to the sync. */
  const startOver = useCallback(() => {
    saved.current = null;
    setUncertainKey(null);
    resetKey();
  }, [resetKey]);
  const clearUncertain = useCallback(() => setUncertainKey(null), []);
  return { ...commit, uncertainKey, startOver, clearUncertain };
}

export interface HoldVars {
  cart: CartRef | null;
  customerName: string;
  /** The cart lines once the server priced them (discounts are sent as money). */
  draft: () => Promise<Pick<CheckoutDraft, "customerId" | "lines">>;
}

/** "Lưu giỏ · F4" / "Lưu thay đổi · F4": parks the cart; a saved cart holds no stock. */
export function useHoldCart(after: (cart: PosCart) => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: async (v: HoldVars, { idempotencyKey }) => {
      const d = await v.draft();
      const body = cartBody(d.customerId, d.lines);
      const cart = v.cart;
      return translated(() =>
        cart === null
          ? client.post("/pos/carts", body, { idempotencyKey, schema: posCartSchema })
          : client.patch(`/pos/carts/${cart.id}`, body, { ifMatch: etagOf(cart.version), schema: posCartSchema }),
      );
    },
    success: (cart, v) => withDoc("Đã lưu giỏ ", cart.doc_no, ` của ${v.customerName}. Giỏ lưu chưa giữ hàng.`),
    invalidate: [posKeys.all],
    after: (cart) => after(cart),
  });
}

/** "Sao chép": a new open cart priced now. */
export function useCopyCart() {
  const client = useApiClient();
  return useCommit({
    mutationFn: (cart: PosCart, { idempotencyKey }) => translated(() => client.post(`/pos/carts/${cart.id}:copy`, undefined, { idempotencyKey, schema: posCartSchema })),
    success: (copy) => withDoc("Đã sao chép thành giỏ ", copy.doc_no, ". Có thể đổi khách và sửa hàng."),
    invalidate: [posKeys.all],
  });
}

/** "Xóa giỏ": only an open cart, which has no stock or money documents yet. */
export function useDeleteCart(cart: PosCart, after: () => void) {
  const client = useApiClient();
  return useCommit<unknown>({
    mutationFn: () => translated(() => client.delete<unknown>(`/pos/carts/${cart.id}`, { ifMatch: etagOf(cart.version) })),
    success: () => "Đã xóa giỏ hàng.",
    invalidate: [posKeys.all],
    after,
  });
}
