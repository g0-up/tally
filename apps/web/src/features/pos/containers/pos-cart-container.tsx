import { useCallback, useEffect, useId, useLayoutEffect, useRef, type ReactNode } from "react";
import { isApiError } from "../../../shared/api/api-error";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { StatusPill } from "../../../shared/ui/status-pill";
import { toast } from "../../../shared/ui/toast-store";
import { WALK_IN_NAME, useColorMaps } from "../../sales/api/queries";
import { draftToInput, isPriceable } from "../../sales/components/line-items/draft";
import { useLinePricing, type LinePricing } from "../../sales/components/line-items/use-line-pricing";
import { priceSourceLabel } from "../../sales/api/adapters";
import type { PosPaymentMethod, PosTile } from "../../sales/schemas";
import { CART_STATUS_LABELS, checkoutPayments, paymentFigures } from "../api/adapters";
import { useCheckout, useHoldCart } from "../api/mutations";
import { CartPanel } from "../components/cart-panel";
import { CustomerBar } from "../components/customer-bar";
import { PaymentMethodPanel } from "../components/payment-method-panel";
import { PosLayout } from "../components/pos-layout";
import { ShiftFallbackCallout } from "../components/shift-fallback-callout";
import type { PosCartApi, PosCartState } from "../hooks/use-pos-cart";
import { usePosHotkeys } from "../hooks/use-pos-hotkeys";
import { connectivity } from "../offline/connectivity";
import { subscribeOfflineEvents } from "../offline/offline-events";
import { OFFLINE_MESSAGES } from "../offline/offline-sale-service";
import { notSavedMessage, useOfflineCheckout } from "../offline/use-offline-checkout";
import { useOfflineLinePricing } from "../offline/use-offline-line-pricing";
import type { OfflinePos } from "../offline/use-offline-pos";

export interface PosCartContainerProps {
  cart: PosCartApi;
  /** Left column: scan field and tiles. */
  catalog: ReactNode;
  methods: readonly PosPaymentMethod[];
  onFocusScan: () => void;
  /** Connectivity, saved catalog and outbox: without the API the cart sells into the outbox. */
  offline: Pick<OfflinePos, "offline" | "bundle">;
}

export const OFFLINE_HOLD_MESSAGE = "Lưu giỏ cần kết nối mạng.";
export const OFFLINE_SAVED_CART_MESSAGE = "Giỏ đã lưu chỉ thanh toán được khi có mạng. Chọn “Giỏ mới” để bán ngoại tuyến.";
export const UNCERTAIN_CHECKOUT_MESSAGE =
  "Chưa nhận được kết quả thanh toán giỏ này: có thể đã ghi sổ. Khi có mạng, hệ thống tự gửi lại đúng giao dịch đó để kiểm tra (không ghi hai lần); bấm Thanh toán lúc có mạng cũng vậy.";

const NO_GROUPS = [] as const;
const NO_TILES: readonly PosTile[] = [];
const TOTAL_ANNOUNCE_DELAY_MS = 500;

/**
 * Pricing as it was last rendered, and a promise that resolves once every pricing answer for the
 * cart on screen has arrived. "Thanh toán" waits for it, so a quantity typed right before the click
 * (the blur commits it) is sold at the price the server computed for it.
 */
function useSettledPricing(pricing: LinePricing) {
  const latest = useRef(pricing);
  const waiters = useRef<Array<() => void>>([]);
  useLayoutEffect(() => {
    latest.current = pricing;
    if (pricing.pending) return;
    const ready = waiters.current;
    waiters.current = [];
    for (const resolve of ready) resolve();
  });
  return useCallback(async (): Promise<LinePricing> => {
    if (latest.current.pending) await new Promise<void>((resolve) => waiters.current.push(resolve));
    return latest.current;
  }, []);
}

/** Right column of the POS screen: the cart, the customer, the money and the two commands. */
export function PosCartContainer({ cart, catalog, methods, onFocusScan, offline }: PosCartContainerProps) {
  const { state } = cart;
  const colors = useColorMaps();
  const isOffline = offline.offline;
  const serverPricing = useLinePricing({ lines: state.lines, partyId: state.customer?.id ?? null, groups: NO_GROUPS, colors });
  const devicePricing = useOfflineLinePricing(state.lines, offline.bundle?.tiles ?? NO_TILES);
  const pricing = isOffline ? devicePricing : serverPricing;
  const settled = useSettledPricing(pricing);
  const latest = useRef<PosCartState>(state);
  useLayoutEffect(() => {
    latest.current = state;
  });

  const checkout = useCheckout(() => cart.reset());
  const hold = useHoldCart(() => cart.reset());
  const offlineCheckout = useOfflineCheckout(offline.bundle, () => cart.reset());

  // A checkout of this cart got no answer and may have posted: the cart is not sold offline until
  // the server answers. A cleared cart is a new sale; the earlier checkout is left to the sync.
  const { uncertainKey, startOver, clearUncertain } = checkout;
  const empty = state.lines.length === 0;
  useEffect(() => {
    if (empty && uncertainKey !== null) startOver();
  }, [empty, uncertainKey, startOver]);
  const { reset } = cart;
  useEffect(() => {
    if (uncertainKey === null) return;
    return subscribeOfflineEvents((event) => {
      if (event.type !== "intent-settled" || event.key !== uncertainKey) return;
      // Posted by the sync: this cart is sold. Refused: nothing posted, the cart can be sold again.
      if (event.posted) reset();
      else clearUncertain();
    });
  }, [uncertainKey, reset, clearUncertain]);

  // Transfer and credit wait for the server: going offline clears them from the cart on screen.
  const { setBank, setCredit } = cart;
  useEffect(() => {
    if (!isOffline) return;
    setBank(null);
    setCredit(false);
  }, [isOffline, setBank, setCredit]);

  // A checkout that could not reach the API makes the page check the connection.
  useEffect(() => {
    if (isApiError(checkout.error) && checkout.error.isNetwork) connectivity.reportFailure();
  }, [checkout.error]);

  const linesOf = useCallback(
    (p: LinePricing, s: PosCartState) => {
      if (s.lines.some((l) => !isPriceable(l))) throw new Error("Nhập số lượng và đơn vị cho mọi dòng hàng.");
      return draftToInput(s.lines, { idOf: colors.idOf, listPrice: (k) => p.listPrice(k)?.amount ?? null, flavorPrice: p.flavorPrice, groups: NO_GROUPS });
    },
    [colors],
  );

  const pay = useCallback(() => {
    const s = latest.current;
    if (s.lines.length === 0) return;
    if (isOffline) {
      if (uncertainKey !== null) {
        toast.error(notSavedMessage(OFFLINE_MESSAGES.uncertain));
        return;
      }
      if (s.cart !== null) {
        toast.error(notSavedMessage(OFFLINE_SAVED_CART_MESSAGE));
        return;
      }
      void offlineCheckout.run({ lines: s.lines, customerId: s.customer?.id ?? null, cash: s.cash ?? "", bank: s.bank ?? "", credit: s.credit, idOf: colors.idOf });
      return;
    }
    void checkout.run({
      cart: s.cart,
      draft: async () => {
        const p = await settled();
        const now = latest.current;
        const total = p.totals?.total_amount;
        if (total === undefined) throw new Error("Chưa tính được tổng tiền. Thử lại.");
        const cash = now.cash ?? "";
        const bank = now.bank ?? "";
        if (dec(bank || "0").gt(dec(total))) throw new Error("Chuyển khoản không được lớn hơn số khách cần trả.");
        if (dec(paymentFigures(total, cash, bank).owe).gt(0)) {
          if (now.customer === null) throw new Error("Không bán chịu cho khách lẻ. Chọn khách có hồ sơ công nợ.");
          if (!now.credit) throw new Error("Khách trả chưa đủ. Chọn “Bán chịu phần còn lại” nếu được phép.");
        }
        return { customerId: now.customer?.id ?? null, lines: linesOf(p, now), payments: checkoutPayments(methods, total, cash, bank) };
      },
    });
  }, [isOffline, uncertainKey, offlineCheckout, colors, checkout, settled, linesOf, methods]);

  const park = useCallback(() => {
    const s = latest.current;
    if (s.lines.length === 0) return;
    if (isOffline) {
      toast.error(notSavedMessage(OFFLINE_HOLD_MESSAGE));
      return;
    }
    void hold.run({
      cart: s.cart,
      customerName: s.customer?.name ?? WALK_IN_NAME,
      draft: async () => {
        const p = await settled();
        const now = latest.current;
        return { customerId: now.customer?.id ?? null, lines: linesOf(p, now) };
      },
    });
  }, [isOffline, hold, settled, linesOf]);

  usePosHotkeys({ focusScan: onFocusScan, hold: park, pay });

  const holdReasonId = useId();
  const canSell = state.lines.length > 0;
  const total: Money | null = pricing.totals?.total_amount ?? null;
  // Read once the total stops moving, so typing a quantity does not announce every keystroke.
  const settledTotal = useDebounce(canSell ? total : null, TOTAL_ANNOUNCE_DELAY_MS);
  const firstKey = state.lines[0]?.key;
  const source = firstKey === undefined ? null : pricing.listPrice(firstKey)?.price_source;

  return (
    <>
      <ShiftFallbackCallout error={checkout.error} />
      {/* Always mounted so the warnings below are read when they appear. */}
      <div aria-live="polite">
        {uncertainKey !== null ? (
          <Callout tone="warn" style={{ marginBottom: 16 }}>
            {UNCERTAIN_CHECKOUT_MESSAGE}
          </Callout>
        ) : null}
        {isOffline && state.customer !== null ? (
          <Callout tone="warn" style={{ marginBottom: 16 }}>
            {OFFLINE_MESSAGES.customer}
          </Callout>
        ) : null}
      </div>
      <PosLayout
        catalog={catalog}
        cart={
          <>
            <div className="card-h" style={{ margin: 0 }}>
              <h2>Giỏ hàng {state.cart ? state.cart.docNo : "mới"}</h2>
              {state.cart ? (
                <>
                  <StatusPill label={CART_STATUS_LABELS.open} />{" "}
                  <Button variant="line" size="sm" onClick={() => cart.reset()}>
                    Giỏ mới
                  </Button>
                </>
              ) : null}
            </div>
            <CustomerBar customer={state.customer} onChange={cart.setCustomer} priceSource={source ? priceSourceLabel(source, null) : null} />
            <CartPanel lines={state.lines} pricing={pricing} colors={colors} onChange={cart.update} onPickColor={cart.pickColor} onSplit={cart.split} onRemove={cart.remove} />
            <div>
              <span style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: ".6px" }}>Khách cần trả</span>
              <div className="cart-total">{vnd(total ?? "0")}</div>
              <div className="sr-only" role="status">
                {settledTotal === null ? null : `Khách cần trả ${vnd(settledTotal)}`}
              </div>
            </div>
            <PaymentMethodPanel
              total={total}
              cash={state.cash}
              bank={state.bank}
              onCash={cart.setCash}
              onBank={cart.setBank}
              onChip={cart.chip}
              credit={state.credit}
              onCredit={cart.setCredit}
              namedCustomer={state.customer !== null}
              mode={isOffline ? "offline" : "online"}
            />
            <ButtonRow>
              <Button variant="primary" disabled={!canSell} pending={checkout.pending || offlineCheckout.pending} aria-keyshortcuts="F9" onClick={pay}>
                Thanh toán · F9
              </Button>
              <Button
                variant="line"
                disabled={!canSell || isOffline}
                pending={hold.pending}
                aria-keyshortcuts="F4"
                aria-describedby={isOffline ? holdReasonId : undefined}
                onClick={park}
              >
                {state.cart ? "Lưu thay đổi" : "Lưu giỏ"} · F4
              </Button>
            </ButtonRow>
            {isOffline ? (
              <p className="hint" id={holdReasonId}>
                {OFFLINE_HOLD_MESSAGE}
              </p>
            ) : null}
          </>
        }
      />
    </>
  );
}
