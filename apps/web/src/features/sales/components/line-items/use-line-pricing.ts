import { useMemo } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../../shared/api/api-context";
import type { Money } from "../../../../shared/domain/money";
import { dec, sumDec, toDecimalString } from "../../../../shared/lib/decimal";
import { useDebounce } from "../../../../shared/hooks/use-debounce";
import type { ColorMaps } from "../../api/queries";
import { calculatedDocumentSchema, listOf, resolvedPriceSchema, type CalculatedDocument, type MixGroup, type PriceSource, type ResolvedPrice } from "../../schemas";
import { discountAmount, flavorPieces, isPriceable, keptDiscount, type LineDraft } from "./draft";

// Server pricing of a draft (the client never computes a price): `POST /pricing/quote` resolves
// the list price of every line, `POST /pricing/calculate` prices the whole document (typed prices,
// discounts, promotions, tax, rounding). Both are batched for all lines, debounced 250 ms and keep
// their last answer while the next one is on its way.

export const PRICING_DEBOUNCE_MS = 250;

/** One priced row of a request and the draft line it belongs to (a mix line has one per flavor). */
interface Entry {
  key: string;
  /** Flavor SKU of a mix line; absent for an item line. */
  flavor?: string;
}

interface PricedRow {
  sku_id: string;
  color_id: string | null;
  unit_id: string;
  qty: string;
}

function priceRows(lines: readonly LineDraft[], groups: readonly MixGroup[], idOf: ColorMaps["idOf"]): { entries: Entry[]; rows: PricedRow[] } {
  const entries: Entry[] = [];
  const rows: PricedRow[] = [];
  for (const l of lines) {
    if (l.kind === "item") {
      if (!isPriceable(l)) continue;
      entries.push({ key: l.key });
      rows.push({ sku_id: l.skuId, color_id: l.color === null ? null : idOf(l.color), unit_id: l.unitId, qty: l.qty });
      continue;
    }
    const group = groups.find((g) => g.id === l.groupId);
    if (!group) continue;
    for (const [skuId, parts] of Object.entries(l.parts)) {
      // A flavor not chosen yet is still priced for one part so the pack price can be shown.
      const p = parts !== null && dec(parts).gt(0) ? parts : "1";
      entries.push({ key: l.key, flavor: skuId });
      rows.push({ sku_id: skuId, color_id: null, unit_id: group.piece_unit_id, qty: flavorPieces(p, group) });
    }
  }
  return { entries, rows };
}

export interface LinePricing {
  /** List price (per unit of the line) and its source, by line key. */
  listPrice: (key: string) => ResolvedPrice | null;
  /** List price of one base piece of a mix flavor. */
  flavorPrice: (key: string, skuId: string) => Money | null;
  /** Amount of a line after its discount (sum of flavors for a mix line), from the server. */
  lineAmount: (key: string) => Money | null;
  /** Gross amount (before discount) of a line. */
  grossAmount: (key: string) => Money | null;
  /** The server reports the price after discount under the SKU minimum. */
  belowMin: (key: string) => boolean;
  /** Document totals from the server; undefined until the first answer. */
  totals: CalculatedDocument | undefined;
  /** Some answer is still on its way. */
  pending: boolean;
}

interface Options {
  lines: readonly LineDraft[];
  /** Customer whose price lists apply; null for a walk-in buyer (retail prices). */
  partyId: string | null;
  groups: readonly MixGroup[];
  colors: ColorMaps;
  /** Skip the document calculation (the quote preset's retail column needs list prices only). */
  listOnly?: boolean;
}

export function useLinePricing({ lines, partyId, groups, colors, listOnly = false }: Options): LinePricing {
  const client = useApiClient();

  const quoteReq = useMemo(() => {
    const { entries, rows } = priceRows(lines, groups, colors.idOf);
    return JSON.stringify({ entries, body: { party_id: partyId, lines: rows } });
  }, [lines, groups, colors, partyId]);
  const quoteKey = useDebounce(quoteReq, PRICING_DEBOUNCE_MS);
  const quote = useQuery({
    queryKey: ["sales", "pricing", "quote", quoteKey],
    queryFn: async ({ signal }) => {
      const { entries, body } = JSON.parse(quoteKey) as { entries: Entry[]; body: { lines: PricedRow[] } };
      const r = await client.post("/pricing/quote", body, { schema: listOf(resolvedPriceSchema), signal, silent: true });
      return { entries, prices: r.data };
    },
    enabled: (JSON.parse(quoteKey) as { entries: Entry[] }).entries.length > 0,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
    meta: { silent: true },
  });

  const priced = useMemo(() => {
    const item = new Map<string, ResolvedPrice>();
    const flavor = new Map<string, Money>();
    const data = quote.data;
    if (data) {
      data.entries.forEach((e, i) => {
        const p = data.prices[i];
        if (!p) return;
        if (e.flavor === undefined) item.set(e.key, p);
        // Flavor rows were priced per piece unit: amount is the price of one piece.
        else flavor.set(`${e.key}|${e.flavor}`, p.amount);
      });
    }
    return { item, flavor };
  }, [quote.data]);

  const calcReq = useMemo(() => {
    if (listOnly) return "";
    const entries: Entry[] = [];
    const rows: Array<PricedRow & { unit_price: Money | null; discount_amount: Money | null }> = [];
    for (const l of lines) {
      if (l.kind === "item") {
        if (!isPriceable(l)) continue;
        const price = l.price ?? priced.item.get(l.key)?.amount ?? null;
        const discount =
          price !== null && dec(l.discountPct).gt(0) ? (keptDiscount(l.savedDiscount, l.discountPct, dec(l.qty).times(dec(price))) ?? discountAmount(l.qty, price, l.discountPct)) : null;
        entries.push({ key: l.key });
        rows.push({ sku_id: l.skuId, color_id: l.color === null ? null : colors.idOf(l.color), unit_id: l.unitId, qty: l.qty, unit_price: l.price, discount_amount: discount });
        continue;
      }
      const group = groups.find((g) => g.id === l.groupId);
      if (!group) continue;
      for (const [skuId, parts] of Object.entries(l.parts)) {
        if (parts === null || !dec(parts).gt(0)) continue;
        const pieces = flavorPieces(parts, group);
        const price = priced.flavor.get(`${l.key}|${skuId}`) ?? null;
        const discount = price !== null && dec(l.discountPct).gt(0) ? discountAmount(pieces, price, l.discountPct) : null;
        entries.push({ key: l.key, flavor: skuId });
        rows.push({ sku_id: skuId, color_id: null, unit_id: group.piece_unit_id, qty: pieces, unit_price: null, discount_amount: discount });
      }
    }
    return JSON.stringify({ entries, body: { party_id: partyId, lines: rows } });
  }, [listOnly, lines, groups, colors, partyId, priced]);
  const calcKey = useDebounce(calcReq, PRICING_DEBOUNCE_MS);
  const calc = useQuery({
    queryKey: ["sales", "pricing", "calculate", calcKey],
    queryFn: async ({ signal }) => {
      const { entries, body } = JSON.parse(calcKey) as { entries: Entry[]; body: unknown };
      const doc = await client.post("/pricing/calculate", body, { schema: calculatedDocumentSchema, signal, silent: true });
      return { entries, doc };
    },
    enabled: calcKey !== "" && (JSON.parse(calcKey) as { entries: Entry[] }).entries.length > 0,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
    meta: { silent: true },
  });

  return useMemo(() => {
    const amounts = new Map<string, { net: Money[]; gross: Money[]; below: boolean }>();
    const data = calc.data;
    if (data) {
      for (const line of data.doc.lines) {
        if (line.draft_index === null) continue;
        const e = data.entries[line.draft_index];
        if (!e) continue;
        const a = amounts.get(e.key) ?? { net: [], gross: [], below: false };
        a.net.push(line.line_amount);
        a.gross.push(line.gross_amount);
        a.below = a.below || line.below_min_price;
        amounts.set(e.key, a);
      }
    }
    const total = (xs: readonly Money[] | undefined) => (xs && xs.length ? (toDecimalString(sumDec(xs)) as Money) : null);
    return {
      listPrice: (key) => priced.item.get(key) ?? null,
      flavorPrice: (key, skuId) => priced.flavor.get(`${key}|${skuId}`) ?? null,
      lineAmount: (key) => total(amounts.get(key)?.net),
      grossAmount: (key) => total(amounts.get(key)?.gross),
      belowMin: (key) => amounts.get(key)?.below ?? false,
      totals: data?.doc,
      pending: quote.isFetching || calc.isFetching || quoteKey !== quoteReq || calcKey !== calcReq,
    };
  }, [priced, calc.data, quote.isFetching, calc.isFetching, quoteKey, quoteReq, calcKey, calcReq]);
}

export type { PriceSource };
