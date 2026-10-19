import type { ColorCode } from "../../../shared/domain/color";
import { isColorCode } from "../../../shared/domain/color";
import { toMoney, type Money } from "../../../shared/domain/money";
import { Decimal, dec, sumDec, toDecimalString } from "../../../shared/lib/decimal";
import type { ColorMaps } from "../../sales/api/queries";
import type { ItemDraft } from "../../sales/components/line-items/draft";
import type { PosCart, PosPaymentMethod, PosTile } from "../../sales/schemas";

// Pure helpers of the POS screen: what a tile offers, the cash chips and how the money the
// customer hands over becomes the payments of the checkout command.

export const CART_STATUS_LABELS: Readonly<Record<PosCart["status"], string>> = { open: "Đang soạn", sold: "Đã bán" };

/** Product name of a cart line ("SP001 · Bánh quy bơ" → "Bánh quy bơ"). */
export function cartLineName(line: Pick<ItemDraft, "skuLabel">): string {
  const i = line.skuLabel.indexOf(" · ");
  return i >= 0 ? line.skuLabel.slice(i + 3) : line.skuLabel;
}

/** Sellable quantity of a tile in its base unit (all colors). */
export function tileAvailable(tile: PosTile): string {
  return toDecimalString(sumDec(tile.stock.map((s) => s.qty)));
}

/** Colors a tile can still be sold in, for its dots and the auto-pick of a single color. */
export function tileColors(tile: PosTile, codeOf: ColorMaps["codeOf"]): ColorCode[] {
  const out: ColorCode[] = [];
  for (const s of tile.stock) {
    if (s.color_id === null || !dec(s.qty).gt(0)) continue;
    const code = codeOf(s.color_id);
    if (isColorCode(code) && !out.includes(code)) out.push(code);
  }
  return out;
}

const CHIP_STEPS = [50_000, 100_000];
const BIG_NOTE = 500_000;
const MAX_CHIPS = 4;

/** Cash chips: the exact total, the total rounded up to 50k and 100k, and 500k; positive, unique, at most 4. */
export function cashChips(total: Money | string): Money[] {
  const t = dec(total);
  if (!t.gt(0)) return [];
  const values = [t, ...CHIP_STEPS.map((step) => t.div(step).toDecimalPlaces(0, Decimal.ROUND_CEIL).times(step)), dec(BIG_NOTE)];
  const out: Money[] = [];
  for (const v of values) {
    const s = toMoney(v);
    if (v.gt(0) && !out.includes(s)) out.push(s);
  }
  return out.slice(0, MAX_CHIPS);
}

export interface PaymentFigures {
  /** Cash plus transfer the customer hands over. */
  paid: Money;
  /** Cash handed back (paid above the total). */
  change: Money;
  /** Part of the total not paid now. */
  owe: Money;
}

export function paymentFigures(total: Money | string, cash: string, bank: string): PaymentFigures {
  const paid = dec(cash || "0").plus(dec(bank || "0"));
  const t = dec(total);
  const zero = dec(0);
  return {
    paid: toMoney(paid),
    change: toMoney(paid.gt(t) ? paid.minus(t) : zero),
    owe: toMoney(t.gt(paid) ? t.minus(paid) : zero),
  };
}

/** The cash value a chip puts in "Tiền mặt khách đưa": the chip minus what goes by transfer. */
export function cashForChip(chip: Money, bank: string): Money {
  const v = dec(chip).minus(dec(bank || "0"));
  return toMoney(v.gt(0) ? v : dec(0));
}

export interface CheckoutPayments {
  payments: { payment_method_id: string; amount: Money }[];
  cash_tendered_amount?: Money;
}

/**
 * The payments of a checkout: the transfer as typed, the cash part capped at what is still due
 * (the rest is change) and the cash tendered so the server records the change. Throws the message
 * to show when a method the customer used is not set up.
 */
export function checkoutPayments(methods: readonly PosPaymentMethod[], total: Money | string, cash: string, bank: string): CheckoutPayments {
  const bankAmount = dec(bank || "0");
  const cashAmount = dec(cash || "0");
  const out: CheckoutPayments = { payments: [] };
  if (bankAmount.gt(0)) {
    const method = methods.find((m) => m.active && !m.cash && m.fund_kind === "bank");
    if (!method) throw new Error("Chưa có hình thức chuyển khoản đang dùng. Gửi yêu cầu hỗ trợ ở Thiết lập › Thuê bao để bật lại.");
    out.payments.push({ payment_method_id: method.id, amount: toMoney(bankAmount) });
  }
  const due = dec(total).minus(bankAmount);
  const cashPart = cashAmount.gt(due) ? due : cashAmount;
  if (cashPart.gt(0)) {
    const method = methods.find((m) => m.active && m.cash);
    if (!method) throw new Error("Chưa có hình thức tiền mặt đang dùng. Gửi yêu cầu hỗ trợ ở Thiết lập › Thuê bao để bật lại.");
    out.payments.push({ payment_method_id: method.id, amount: toMoney(cashPart) });
    out.cash_tendered_amount = toMoney(cashAmount);
  }
  return out;
}
