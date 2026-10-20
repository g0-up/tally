import { useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { isColorCode, type ColorCode, type ColorKey } from "../../../shared/domain/color";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import type { ComboItem } from "../../../shared/ui/combobox";
import { skuComboItem } from "./adapters";
import { attributeSchema, attributeSetSchema, barcodeSchema, categorySchema, colorSchema, mixGroupSchema, skuListItemSchema, unitSchema, type ProductType, type SkuListItem, type Unit } from "../schemas";

// Read side of the catalog screens. Every key starts with "catalog" so a posting that changes
// prices, costs or stock refreshes product lists with one prefix.

export const catalogKeys = {
  all: ["catalog"] as const,
  lookup: (...rest: readonly unknown[]) => ["catalog", "lookup", ...rest] as const,
};

export const skuKeys = {
  all: ["catalog", "skus"] as const,
  list: (params: Readonly<Record<string, string>>) => ["catalog", "skus", "list", params] as const,
  byIds: (ids: readonly string[]) => ["catalog", "skus", "by-id", ...ids] as const,
  barcodes: (skuId: string) => ["catalog", "skus", "barcodes", skuId] as const,
};

export const mixGroupKeys = {
  all: ["catalog", "mix-groups"] as const,
};

const LOOKUP_STALE_MS = 5 * 60_000;
/** Most SKUs one `GET /skus?id=` request may name. */
const MAX_SKU_IDS = 200;
/** Candidates one picker search returns. */
const PICKER_LIMIT = 50;

export function useUnits() {
  const client = useApiClient();
  const query = useQuery({
    queryKey: catalogKeys.lookup("units"),
    queryFn: ({ signal }) => client.get("/units", { schema: listOf(unitSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
  });
  return useMemo(() => {
    const all = query.data?.data ?? [];
    const byId = new Map<string, Unit>(all.map((u) => [u.id, u]));
    return {
      /** Active units, for pickers. */
      active: all.filter((u) => u.active),
      name: (unitId: string | null | undefined) => (unitId ? (byId.get(unitId)?.name ?? "") : ""),
      /** Id of the unit with this name (case-insensitive), for the prototype defaults ("hộp", "thùng"). */
      idByName: (name: string) => all.find((u) => u.active && u.name.toLocaleLowerCase("vi") === name.toLocaleLowerCase("vi"))?.id ?? null,
      isLoading: query.isPending,
    };
  }, [query.data, query.isPending]);
}

export function useCategories() {
  const client = useApiClient();
  return useQuery({
    queryKey: catalogKeys.lookup("categories"),
    queryFn: ({ signal }) => client.get("/categories", { schema: listOf(categorySchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((c) => c.active),
  });
}

export interface ColorDirectory {
  /** API color id → fixed color code; "none" for no color. */
  codeOf: (colorId: string | null | undefined) => ColorKey;
  /** Fixed color code → API color id. */
  idOf: (color: ColorCode | null) => string | null;
}

/** The fixed product colors keyed both ways (ids are per tenant, codes are fixed). */
export function useColorDirectory(): ColorDirectory {
  const client = useApiClient();
  const { data } = useQuery({
    queryKey: catalogKeys.lookup("colors"),
    queryFn: ({ signal }) => client.get("/colors", { schema: listOf(colorSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
  });
  return useMemo(() => {
    const byId = new Map<string, ColorCode>();
    const byCode = new Map<ColorCode, string>();
    for (const c of data?.data ?? []) {
      if (!isColorCode(c.code)) continue;
      byId.set(c.id, c.code);
      byCode.set(c.code, c.id);
    }
    return {
      codeOf: (colorId) => (colorId ? (byId.get(colorId) ?? "none") : "none"),
      idOf: (color) => (color === null ? null : (byCode.get(color) ?? null)),
    };
  }, [data]);
}

export function useAttributeSet(attributeSetId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: catalogKeys.lookup("attribute-set", attributeSetId),
    queryFn: ({ signal }) => client.get(`/attribute-sets/${attributeSetId}`, { schema: attributeSetSchema, signal }),
    enabled: attributeSetId !== null,
    staleTime: LOOKUP_STALE_MS,
  });
}

export function useAttributes(enabled: boolean) {
  const client = useApiClient();
  return useQuery({
    queryKey: catalogKeys.lookup("attributes"),
    queryFn: ({ signal }) => client.get("/attributes", { schema: listOf(attributeSchema), signal }),
    enabled,
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data,
  });
}

export interface SkuListParams {
  q?: string;
  category_id?: string;
  type?: string;
  status?: string;
}

/** `GET /skus` with the screen columns (prices, costs, stock by color), paged by cursor. */
export function useSkuList(params: SkuListParams, limit = 100) {
  const clean = useMemo(() => Object.fromEntries(Object.entries(params).filter(([, v]) => v !== undefined && v !== "")) as Record<string, string>, [params]);
  return useCursorList({ queryKey: skuKeys.list(clean), path: "/skus", item: skuListItemSchema, params: clean, limit });
}

/** The listed SKUs with these ids (mix-group flavors), in one request of at most 200 ids. */
export function useSkusByIds(ids: readonly string[]) {
  const client = useApiClient();
  const sorted = useMemo(() => [...new Set(ids)].sort().slice(0, MAX_SKU_IDS), [ids]);
  return useQuery({
    queryKey: skuKeys.byIds(sorted),
    queryFn: ({ signal }) => client.get("/skus", { query: { id: sorted, limit: MAX_SKU_IDS }, schema: listOf(skuListItemSchema), signal }),
    enabled: sorted.length > 0,
    select: (r) => new Map(r.data.map((s) => [s.id, s])),
  });
}

/** `GET /skus/{id}/barcodes`: every barcode of the SKU with the unit a scan of it answers. */
export function useSkuBarcodes(skuId: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: skuKeys.barcodes(skuId),
    queryFn: ({ signal }) => client.get(`/skus/${skuId}/barcodes`, { schema: listOf(barcodeSchema), signal }),
    select: (r) => r.data,
  });
}

export function useMixGroups() {
  const client = useApiClient();
  return useQuery({
    queryKey: mixGroupKeys.all,
    queryFn: ({ signal }) => client.get("/mix-groups", { schema: listOf(mixGroupSchema), signal }),
    select: (r) => r.data,
  });
}

export interface SkuPicker {
  /** Combobox `source` over active SKUs of the given type (`GET /skus?q=`). */
  source: { key: string; search: (ctx: { client: ReturnType<typeof useApiClient>; query: string; signal: AbortSignal }) => Promise<readonly ComboItem[]> };
  /** The full list row of a SKU the picker has shown; call it from the pick handler. */
  picked: (skuId: string) => SkuListItem | undefined;
}

/** Remote SKU picker that also keeps the full rows it showed, so a pick carries units and prices. */
export function useSkuPicker(type: ProductType | null): SkuPicker {
  const seen = useRef(new Map<string, SkuListItem>());
  return useMemo(
    () => ({
      source: {
        key: `catalog-skus-${type ?? "all"}`,
        search: async ({ client, query, signal }) => {
          const r = await client.get("/skus", { query: { q: query.trim(), type, status: "active", limit: PICKER_LIMIT }, schema: listOf(skuListItemSchema), signal });
          for (const s of r.data) seen.current.set(s.id, s);
          return r.data.map(skuComboItem);
        },
      },
      picked: (skuId) => seen.current.get(skuId),
    }),
    [type],
  );
}
