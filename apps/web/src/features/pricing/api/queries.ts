import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { cursorPageSchema } from "../../../shared/hooks/use-cursor-list";
import { customerGroupSchema, priceChangeSchema, priceListItemSchema, priceListSchema } from "../schemas";

// Read side of the pricing screens. Every key starts with "pricing" so a price edit refreshes the
// lists, their rows and the history with one prefix.

export const pricingKeys = {
  all: ["pricing"] as const,
};

export const priceListKeys = {
  all: ["pricing", "price-lists"] as const,
  items: (listId: string, skuId: string | null) => ["pricing", "price-lists", listId, "items", skuId] as const,
};

export const priceChangeKeys = {
  all: ["pricing", "price-changes"] as const,
  list: (skuId: string | null) => ["pricing", "price-changes", skuId] as const,
};

/** Price-change rows read for the history table; grouped, the table shows the newest 15 edits. */
const HISTORY_LIMIT = 60;
/** Rows of one list read at once (tiers of group lists, color prices of one SKU). */
const ITEMS_LIMIT = 200;

/** All price lists, base lists first then group, customer and season lists by priority. */
export function usePriceLists() {
  const client = useApiClient();
  return useQuery({
    queryKey: priceListKeys.all,
    queryFn: ({ signal }) => client.get("/price-lists", { schema: listOf(priceListSchema), signal }),
    select: (r) => r.data,
  });
}

/** Rows of one price list, optionally of one SKU (first page of 200). */
export function usePriceListItems(listId: string | null, skuId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: priceListKeys.items(listId ?? "", skuId),
    queryFn: ({ signal }) =>
      client.get(`/price-lists/${listId}/items`, { query: { sku_id: skuId, limit: ITEMS_LIMIT }, schema: cursorPageSchema(priceListItemSchema), signal }),
    enabled: listId !== null,
    select: (r) => r.data,
  });
}

/** Newest sell-price changes of one SKU (`/skus/{id}/price-history`) or of every SKU (`/price-changes`). */
export function usePriceChanges(skuId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: priceChangeKeys.list(skuId),
    queryFn: ({ signal }) =>
      client.get(skuId === null ? "/price-changes" : `/skus/${skuId}/price-history`, { query: { limit: HISTORY_LIMIT }, schema: cursorPageSchema(priceChangeSchema), signal }),
    select: (r) => r.data,
  });
}

export function useCustomerGroups() {
  const client = useApiClient();
  return useQuery({
    queryKey: [...pricingKeys.all, "customer-groups"],
    queryFn: ({ signal }) => client.get("/customer-groups", { query: { active: true }, schema: listOf(customerGroupSchema), signal }),
    staleTime: 5 * 60_000,
    select: (r) => r.data,
  });
}
