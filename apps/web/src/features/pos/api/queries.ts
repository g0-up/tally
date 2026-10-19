import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import type { HttpClient } from "../../../shared/api/http";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { listOf, posBootstrapSchema, posCartSchema, resolvedPriceSchema, type PosTile } from "../../sales/schemas";

// Read side of the POS screen. Every key starts with "pos" so a posting refreshes the whole
// screen (tiles, saved carts) with one prefix.

export const posKeys = {
  all: ["pos"] as const,
  bootstrap: () => ["pos", "bootstrap"] as const,
  carts: (status: "open" | "sold") => ["pos", "carts", status] as const,
  cart: (id: string) => ["pos", "carts", "detail", id] as const,
  tilePrices: (partyId: string, skuIds: readonly string[]) => ["pos", "tile-prices", partyId, ...skuIds] as const,
};

const CART_PAGE = 50;

/** Settings, payment methods, the open shift and the sellable tiles with retail price and stock. */
export function usePosBootstrap() {
  const client = useApiClient();
  return useQuery({
    queryKey: posKeys.bootstrap(),
    queryFn: ({ signal }) => client.get("/pos/bootstrap", { schema: posBootstrapSchema, signal }),
    staleTime: 15_000,
  });
}

/** Saved carts of one status, newest first (the list carries no lines). */
export function usePosCarts(status: "open" | "sold") {
  return useCursorList({ queryKey: posKeys.carts(status), path: "/pos/carts", item: posCartSchema, params: { status }, limit: CART_PAGE });
}

export function posCartQuery(client: HttpClient, id: string) {
  return {
    queryKey: posKeys.cart(id),
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const r = await client.request("GET", `/pos/carts/${id}`, { schema: posCartSchema, signal });
      return { ...r.data, etag: r.etag };
    },
    staleTime: 30_000,
  };
}

/** Lines of the listed carts for the "Hàng" column, one request per cart. */
export function useCartDetails(ids: readonly string[]) {
  const client = useApiClient();
  return useQueries({ queries: ids.map((id) => posCartQuery(client, id)) });
}

/**
 * Tile prices for a named customer (customer → group price list → retail, resolved by the server);
 * a walk-in buyer sees the retail price the bootstrap already carries.
 */
export function useTilePrices(tiles: readonly PosTile[], partyId: string | null) {
  const client = useApiClient();
  const skuIds = tiles.map((t) => t.sku_id);
  return useQuery({
    queryKey: posKeys.tilePrices(partyId ?? "", skuIds),
    queryFn: async ({ signal }) => {
      const body = { party_id: partyId, lines: tiles.map((t) => ({ sku_id: t.sku_id, unit_id: t.unit_id, qty: "1" })) };
      const r = await client.post("/pricing/quote", body, { schema: listOf(resolvedPriceSchema), signal, silent: true });
      return { partyId, prices: new Map(tiles.map((t, i) => [t.sku_id, r.data[i] ?? null])) };
    },
    enabled: partyId !== null && tiles.length > 0,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
    meta: { silent: true },
  });
}
