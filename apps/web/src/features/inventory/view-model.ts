import { COLORS, type ColorKey } from "../../shared/domain/color";
import { addDays, monthStart } from "../../shared/lib/dates";
import { dec } from "../../shared/lib/decimal";
import { colorKeyOf } from "./api/adapters";
import type { ColorStock, Count, Reason, StockCard, StockRow } from "./schemas";

// View helpers shared by the Kho tables, dialogs and pages.

/** Which color the "Màu" chips show: every color, one color, or stock without one. */
export type ColorFilter = "all" | ColorKey;

/** The colors a row holds, in the fixed color order with untagged stock last. */
export function stockColors(row: StockRow): ColorStock[] {
  const order = (c: ColorStock) => {
    const k = colorKeyOf(c.color_code);
    return k === "none" ? COLORS.length : COLORS.findIndex((x) => x.key === k);
  };
  return row.colors.filter((c) => dec(c.on_hand).gt(0)).sort((a, b) => order(a) - order(b));
}

/** A row is "có màu" when any of its stock carries a color. */
export const hasColors = (row: StockRow) => stockColors(row).some((c) => colorKeyOf(c.color_code) !== "none");

/** Stock card link of a SKU, optionally narrowed to one color. */
export const stockCardHref = (skuId: string, color?: ColorKey) => `/inventory/stock-card?sku=${encodeURIComponent(skuId)}${color ? `&color=${color}` : ""}`;

/** "Toàn kho" or the category, plus "đếm mù". */
export function countScopeLabel(c: Count, categoryName: (id: string | null | undefined) => string | null): string {
  const scope = c.scope === "category" ? (categoryName(c.category_id) ?? "Nhóm hàng") : c.scope === "selection" ? "Hàng chọn" : "Toàn kho";
  return `${scope}${c.blind ? " · đếm mù" : ""}`;
}

/** A catalog reason, or typed text when the catalog has none of the kinds the posting accepts. */
export interface ReasonValue {
  reasonId: string;
  text: string;
}

export const EMPTY_REASON: ReasonValue = { reasonId: "", text: "" };

/** True when the value names a reason the posting can carry. */
export const reasonGiven = (v: ReasonValue, reasons: readonly Reason[]) => (reasons.length > 0 ? reasons.some((r) => r.id === v.reasonId) : v.text.trim() !== "");

/** Range chips of the stock card. */
export type CardRange = "today" | "7d" | "month" | "prev" | "all";

/** First day "Từ đầu" reaches; earlier than any store's first posting. */
export const CARD_EPOCH = "2000-01-01";

/** [from, to] of each range chip as of `today`. */
export function cardRanges(today: string): Readonly<Record<CardRange, readonly [string, string]>> {
  const ms = monthStart(today);
  const prevEnd = addDays(ms, -1);
  return { today: [today, today], "7d": [addDays(today, -6), today], month: [ms, today], prev: [monthStart(prevEnd), prevEnd], all: [CARD_EPOCH, today] };
}

/** "Đã khớp sổ và tồn", or where the card and the stock on hand part. */
export function reconcileLabel(card: Pick<StockCard, "reconciled" | "closing" | "on_hand">): string {
  return card.reconciled ? "Đã khớp sổ và tồn" : `Lệch: thẻ kho ${card.closing.display}, tồn thực ${card.on_hand.display}`;
}
