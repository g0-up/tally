import { useCallback, useLayoutEffect, useMemo, useReducer, useRef } from "react";
import { colorLabel, type ColorKey } from "../../../shared/domain/color";
import type { Money } from "../../../shared/domain/money";
import { toQty, type Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { toast } from "../../../shared/ui/toast-store";
import { newItemLine, pickColor, splitColor, type ItemDraft, type LineDraft } from "../../sales/components/line-items/draft";
import type { CartRef } from "../api/mutations";
import { cartLineName, cashForChip } from "../api/adapters";

// State of the cart on the POS screen. Every input on the screen is controlled by this state, so
// the "Thanh toán · F9" click reads what is typed even when it also blurs a field.

export interface PosCustomer {
  id: string;
  name: string;
}

export interface PosCartState {
  lines: ItemDraft[];
  /** Named customer; null sells to "Khách lẻ". */
  customer: PosCustomer | null;
  cash: Money | null;
  bank: Money | null;
  /** "Bán chịu phần còn lại". */
  credit: boolean;
  /** Saved cart being edited; null for a new cart. */
  cart: CartRef | null;
}

/** What a tile or a scan adds: the SKU in the unit tapped or scanned. */
export interface PosProduct {
  skuId: string;
  skuLabel: string;
  unitId: string;
  /** The only color still sellable, picked for the customer; null when there is a choice. */
  autoColor: ColorKey | null;
}

export const EMPTY_CART: PosCartState = { lines: [], customer: null, cash: null, bank: null, credit: false, cart: null };

type Action =
  | { type: "add"; product: PosProduct }
  | { type: "lines"; lines: ItemDraft[] }
  | { type: "line"; line: ItemDraft }
  | { type: "customer"; customer: PosCustomer | null }
  | { type: "cash"; cash: Money | null }
  | { type: "bank"; bank: Money | null }
  | { type: "credit"; credit: boolean }
  | { type: "load"; state: PosCartState }
  | { type: "reset" };

const items = (lines: readonly LineDraft[]): ItemDraft[] => lines.filter((l): l is ItemDraft => l.kind === "item");
const isZero = (qty: Qty | null) => qty !== null && dec(qty).lte(0);

/**
 * Adding a product counts it on the line of the same SKU and unit that has no color yet or
 * the color it would get anyway; otherwise it starts a new line (prototype `posAdd`).
 */
function addProduct(lines: readonly ItemDraft[], p: PosProduct): ItemDraft[] {
  const same = lines.find((l) => l.skuId === p.skuId && l.unitId === p.unitId && (l.color === null || l.color === p.autoColor));
  if (!same) return [...lines, newItemLine({ skuId: p.skuId, skuLabel: p.skuLabel, unitId: p.unitId, color: p.autoColor })];
  const next: ItemDraft = { ...same, qty: toQty(dec(same.qty ?? "0").plus(1)), color: same.color ?? p.autoColor };
  return lines.map((l) => (l.key === same.key ? next : l));
}

export function posCartReducer(state: PosCartState, action: Action): PosCartState {
  switch (action.type) {
    case "add":
      return { ...state, lines: addProduct(state.lines, action.product) };
    case "lines":
      return { ...state, lines: action.lines };
    case "line":
      // A quantity of 0 removes the line (prototype: lines with SL 0 are dropped).
      return {
        ...state,
        lines: isZero(action.line.qty) ? state.lines.filter((l) => l.key !== action.line.key) : state.lines.map((l) => (l.key === action.line.key ? action.line : l)),
      };
    case "customer":
      // Credit belongs to a named customer; changing the buyer asks for it again.
      return { ...state, customer: action.customer, credit: false };
    case "cash":
      return { ...state, cash: action.cash };
    case "bank":
      return { ...state, bank: action.bank };
    case "credit":
      return { ...state, credit: action.credit && state.customer !== null };
    case "load":
      return action.state;
    case "reset":
      return EMPTY_CART;
  }
}

export function usePosCart(initial: PosCartState = EMPTY_CART) {
  const [state, dispatch] = useReducer(posCartReducer, initial);
  // Latest state for the actions, so a handler created in an earlier render still sees every line.
  const latest = useRef(state);
  useLayoutEffect(() => {
    latest.current = state;
  });

  const pick = useCallback((key: string, color: ColorKey) => {
    const r = pickColor(latest.current.lines, key, color);
    dispatch({ type: "lines", lines: items(r.lines) });
    if (r.mergedInto) toast.success(`Đã gộp vào dòng ${cartLineName(r.mergedInto)} ${colorLabel(color)}.`);
  }, []);

  const actions = useMemo(
    () => ({
      add: (product: PosProduct) => dispatch({ type: "add", product }),
      update: (line: ItemDraft) => dispatch({ type: "line", line }),
      setQty: (key: string, qty: Qty | null) => {
        const line = latest.current.lines.find((l) => l.key === key);
        if (line) dispatch({ type: "line", line: { ...line, qty } });
      },
      remove: (key: string) => dispatch({ type: "lines", lines: latest.current.lines.filter((l) => l.key !== key) }),
      pickColor: pick,
      split: (key: string) => dispatch({ type: "lines", lines: items(splitColor(latest.current.lines, key)) }),
      setCustomer: (customer: PosCustomer | null) => dispatch({ type: "customer", customer }),
      setCash: (cash: Money | null) => dispatch({ type: "cash", cash }),
      setBank: (bank: Money | null) => dispatch({ type: "bank", bank }),
      /** A cash chip: the chip value minus what goes by transfer. */
      chip: (value: Money) => dispatch({ type: "cash", cash: cashForChip(value, latest.current.bank ?? "") }),
      setCredit: (credit: boolean) => dispatch({ type: "credit", credit }),
      load: (next: PosCartState) => dispatch({ type: "load", state: next }),
      reset: () => dispatch({ type: "reset" }),
    }),
    [pick],
  );

  return { state, ...actions };
}

export type PosCartApi = ReturnType<typeof usePosCart>;
