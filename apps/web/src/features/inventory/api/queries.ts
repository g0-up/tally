import { useInfiniteQuery, useQueries, useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { cursorPageSchema, useCursorList } from "../../../shared/hooks/use-cursor-list";
import { dec } from "../../../shared/lib/decimal";
import { priceListKeys } from "../../pricing";
import {
  adjustmentListItemSchema,
  colorPriceItemSchema,
  countListItemSchema,
  countSchema,
  lotSchema,
  reasonSchema,
  reservationSchema,
  stockCardSchema,
  stockDetailSchema,
  stockRowSchema,
  type ColorPriceItem,
  type LotState,
} from "../schemas";

// Read side of the Kho screens. Every key starts with "inventory" so a posting refreshes stock,
// lots, counts, adjustments and the stock card with one prefix (the sales and procurement screens
// read their stock under the same prefix).

export const inventoryKeys = {
  all: ["inventory"] as const,
  stock: ["inventory", "stock", "list"] as const,
  stockDetail: (skuId: string) => ["inventory", "stock", skuId] as const,
  card: (params: Readonly<Record<string, string>>) => ["inventory", "stock-card", params] as const,
  lots: (state: string) => ["inventory", "lots", "list", state] as const,
  reservations: ["inventory", "reservations"] as const,
  adjustments: ["inventory", "adjustments", "list"] as const,
  counts: ["inventory", "counts", "list"] as const,
  count: (id: string) => ["inventory", "counts", "detail", id] as const,
  lookup: (...rest: readonly unknown[]) => ["inventory", "lookup", ...rest] as const,
};

/** Dashboard tiles and the notification feed show stock alerts; postings here change them. */
export const ALERT_KEYS = [["dashboard"], ["notifications"]] as const;

const LOOKUP_STALE_MS = 5 * 60_000;
/** Rows one list page holds. */
const PAGE = 50;
/** Rows of a stock card page. */
const CARD_PAGE = 200;
/** Price rows of one SKU read at once (its color prices). */
const PRICE_ROWS = 200;
/** Row details a list shows; mutations invalidate them, so a revisit within this window reuses them. */
const DETAIL_STALE_MS = 30_000;

export interface StockParams {
  q: string;
  categoryId: string | null;
  /** SKUs holding this color. */
  colorId: string | null;
  /** Only SKUs that hold stock by color. */
  coloredOnly: boolean;
  /** Only SKUs holding stock without a color (Không màu); the server rejects it with the two above. */
  uncolored: boolean;
}

/** Tồn kho rows (`GET /stock`) with their color breakdown, filtered by the server. */
export function useStockList({ q, categoryId, colorId, coloredOnly, uncolored }: StockParams) {
  return useCursorList({
    queryKey: inventoryKeys.stock,
    path: "/stock",
    item: stockRowSchema,
    params: { q, category_id: categoryId, color_id: colorId, colored_only: coloredOnly || null, uncolored: uncolored || null },
    limit: PAGE,
  });
}

/** One SKU's stock with its colors and lots. */
export function useStockDetail(skuId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: inventoryKeys.stockDetail(skuId ?? ""),
    queryFn: ({ signal }) => client.get(`/stock/${skuId}`, { schema: stockDetailSchema, signal }),
    enabled: skuId !== null,
  });
}

export interface StockCardParams {
  skuId: string;
  colorId: string | null;
  uncolored: boolean;
  from: string;
  to: string;
  unitId: string | null;
}

/** Thẻ kho of one SKU over a period; the totals come with the first page. */
export function useStockCard(p: StockCardParams | null) {
  const client = useApiClient();
  const params: Record<string, string> =
    p === null
      ? {}
      : {
          sku_id: p.skuId,
          from: p.from,
          to: p.to,
          unit_mode: p.unitId === null ? "auto" : "fixed",
          ...(p.unitId !== null ? { unit_id: p.unitId } : {}),
          ...(p.colorId !== null ? { color_id: p.colorId } : {}),
          ...(p.uncolored ? { uncolored: "true" } : {}),
        };
  const query = useInfiniteQuery({
    queryKey: inventoryKeys.card(params),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => client.get("/stock-card", { query: { ...params, limit: CARD_PAGE, cursor: pageParam }, schema: stockCardSchema, signal }),
    getNextPageParam: (last) => last.next_cursor ?? null,
    enabled: p !== null,
  });
  const pages = query.data?.pages;
  const card = useMemo(() => (pages && pages[0] ? { ...pages[0], rows: pages.flatMap((pg) => pg.rows) } : null), [pages]);
  return {
    card,
    isLoading: query.isPending && p !== null,
    error: query.error,
    hasMore: query.hasNextPage,
    loadMore: () => query.fetchNextPage(),
    loadingMore: query.isFetchingNextPage,
    refetch: query.refetch,
  };
}

/** Which lots "Lô & hạn dùng" lists. */
export type LotFilter = "all" | "soon" | "expired" | "quarantine";

/** Days ahead the "Hết hạn trong 90 ngày" chip looks. */
export const SOON_DAYS = 90;

const LOT_QUERY: Readonly<Record<LotFilter, { state: LotState | "all"; expiring_within?: number }>> = {
  all: { state: "all" },
  soon: { state: "expiring", expiring_within: SOON_DAYS },
  expired: { state: "expired" },
  quarantine: { state: "quarantine" },
};

/** Lots holding stock, FEFO first (`GET /lots`). */
export function useLotList(filter: LotFilter) {
  return useCursorList({ queryKey: inventoryKeys.lots(filter), path: "/lots", item: lotSchema, params: { ...LOT_QUERY[filter] }, limit: PAGE });
}

/** Open reservations (`GET /reservations?status=active`). */
export function useReservationList() {
  return useCursorList({ queryKey: inventoryKeys.reservations, path: "/reservations", item: reservationSchema, params: { status: "active" }, limit: PAGE });
}

/** Phiếu điều chỉnh list, newest first, or the adjustment a `doc_no` names; rows carry a line preview and totals. */
export function useAdjustmentList(docNo: string | null = null) {
  return useCursorList({
    queryKey: inventoryKeys.adjustments,
    path: "/stock-adjustments",
    item: adjustmentListItemSchema,
    params: { doc_no: docNo },
    limit: PAGE,
  });
}

/** Phiên kiểm kê list, newest first, or the count a `doc_no` names; rows carry their line totals. */
export function useCountList(docNo: string | null = null, enabled = true) {
  return useCursorList({ queryKey: inventoryKeys.counts, path: "/stock-counts", item: countListItemSchema, params: { doc_no: docNo }, limit: PAGE, enabled });
}

/** One count with its lines; `system_qty` is absent while a blind count is open. */
export function useCount(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: inventoryKeys.count(id ?? ""),
    queryFn: ({ signal }) => client.get(`/stock-counts/${id}`, { schema: countSchema, signal }),
    enabled: id !== null,
  });
}

/** Active reasons of every kind; the dialogs pick the kinds a posting accepts. */
export function useReasons() {
  const client = useApiClient();
  return useQuery({
    queryKey: inventoryKeys.lookup("reasons"),
    queryFn: ({ signal }) => client.get("/reasons", { query: { active: true }, schema: listOf(reasonSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((x) => x.active),
  });
}

const priceListRefSchema = listOf(z.object({ id: z.string(), kind: z.string() }));

/** Retail and wholesale base list ids, where color prices live. */
function useBasePriceLists() {
  const client = useApiClient();
  return useQuery({
    queryKey: [...priceListKeys.all, "base"],
    queryFn: ({ signal }) => client.get("/price-lists", { schema: priceListRefSchema, signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => ({ retail: r.data.find((l) => l.kind === "retail")?.id ?? null, wholesale: r.data.find((l) => l.kind === "wholesale")?.id ?? null }),
  });
}

/** One color's own base prices; null when the color follows the product price. */
export interface ColorPrice {
  retail: ColorPriceItem["price"] | null;
  wholesale: ColorPriceItem["price"] | null;
}

/** `${skuId}|${colorId}` → the color's own prices, for the SKUs that have any. */
export type ColorPriceMap = ReadonlyMap<string, ColorPrice>;

export const colorPriceKey = (skuId: string, colorId: string | null | undefined) => `${skuId}|${colorId ?? ""}`;

/**
 * Base-unit, single-quantity color rows of the retail and wholesale lists of these SKUs: two requests
 * per SKU, since the price list items filter by one SKU at a time.
 */
export function useColorPrices(skuIds: readonly string[]): ColorPriceMap {
  const client = useApiClient();
  const lists = useBasePriceLists();
  const pairs = useMemo(() => {
    const l = lists.data;
    if (!l) return [];
    return skuIds.flatMap((skuId) => (["retail", "wholesale"] as const).flatMap((kind) => (l[kind] === null ? [] : [{ kind, listId: l[kind], skuId }])));
  }, [lists.data, skuIds]);
  return useQueries({
    queries: pairs.map(({ listId, skuId }) => ({
      queryKey: [...priceListKeys.items(listId, skuId), "colors"],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        client.get(`/price-lists/${listId}/items`, { query: { sku_id: skuId, limit: PRICE_ROWS }, schema: cursorPageSchema(colorPriceItemSchema), signal }),
      staleTime: DETAIL_STALE_MS,
    })),
    combine: (results) => {
      const out = new Map<string, ColorPrice>();
      results.forEach((r, i) => {
        const kind = pairs[i]?.kind;
        if (!kind || !r.data) return;
        for (const it of r.data.data) {
          if (!it.color_id || it.unit_id || dec(it.min_qty).gt(1)) continue;
          const key = colorPriceKey(it.sku_id, it.color_id);
          const cur = out.get(key) ?? { retail: null, wholesale: null };
          out.set(key, { ...cur, [kind]: it.price });
        }
      });
      return out;
    },
  });
}
