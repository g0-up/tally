import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { dec } from "../../../shared/lib/decimal";
import { inventoryKeys, lotSchema } from "../../inventory";
import {
  assemblyDetailSchema,
  assemblyOrderSchema,
  assemblyPreviewSchema,
  costViewSchema,
  disassemblySchema,
  genealogySchema,
  invoiceRefSchema,
  kitListItemSchema,
  kitProgressSchema,
  kitSchema,
  mixGroupSchema,
  recipeSchema,
  type KitType,
} from "../schemas";
import { itemRequestOf } from "../view-model";
import type { AssemblyRequest, RecipeRequest } from "./requests";

// Read side of the Giỏ quà screens. Every key starts with "kits"; a posting also refreshes the
// "inventory" prefix since assembly and disassembly move stock and lots.

export const kitKeys = {
  all: ["kits"] as const,
  list: ["kits", "list"] as const,
  detail: (id: string) => ["kits", "detail", id] as const,
  cost: (id: string) => ["kits", "cost", id] as const,
  activeCost: (id: string, version: number) => ["kits", "active-cost", id, version] as const,
  recipe: (id: string, version: number) => ["kits", "recipe", id, version] as const,
  draftCost: (body: unknown) => ["kits", "draft-cost", body] as const,
  progress: ["kits", "progress"] as const,
  assemblies: ["kits", "assemblies", "list"] as const,
  assembly: (id: string) => ["kits", "assemblies", "detail", id] as const,
  assemblyPreview: (body: unknown) => ["kits", "assemblies", "preview", body] as const,
  disassemblies: ["kits", "disassemblies"] as const,
  genealogy: (lotId: string) => ["kits", "genealogy", lotId] as const,
  lookup: (...rest: readonly unknown[]) => ["kits", "lookup", ...rest] as const,
};

/** Kit lots live with the other lots, so stock postings refresh them too. */
export const kitLotsKey = [...inventoryKeys.all, "lots", "kit"] as const;

const LOOKUP_STALE_MS = 5 * 60_000;
const PAGE = 50;
/** Row details a list shows; mutations invalidate them, so a revisit within this window reuses them. */
const DETAIL_STALE_MS = 30_000;

/** Kits and combos with their latest recipe version (`GET /kits`). */
export function useKitList(type?: KitType, limit = PAGE) {
  return useCursorList({ queryKey: [...kitKeys.list, type ?? "all", limit], path: "/kits", item: kitListItemSchema, params: { type }, limit });
}

/** The listed kits shown so far with their latest recipe (the list carries none). */
export function useKits(ids: readonly string[]) {
  const client = useApiClient();
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: kitKeys.detail(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/kits/${id}`, { schema: kitSchema, signal }),
      staleTime: DETAIL_STALE_MS,
    })),
    combine: (results) => new Map(results.flatMap((r) => (r.data ? [[r.data.product_id, r.data] as const] : []))),
  });
}

/** One kit with its latest recipe. */
export function useKit(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.detail(id ?? ""),
    queryFn: ({ signal }) => client.get(`/kits/${id}`, { schema: kitSchema, signal }),
    enabled: id !== null,
  });
}

/** Economics of each kit's latest recipe: component prices, cost, kit price and buildable kits. */
export function useKitCosts(ids: readonly string[]) {
  const client = useApiClient();
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: kitKeys.cost(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/kits/${id}/preview`, { schema: costViewSchema, signal }),
      staleTime: DETAIL_STALE_MS,
    })),
    combine: (results) => new Map(results.flatMap((r, i) => (r.data ? [[ids[i] as string, r.data] as const] : []))),
  });
}

/** Recipe versions of one kit, for the "Phiên bản cũ" line a card opens on demand. */
export function useRecipeVersions(kitId: string, versions: readonly number[]) {
  const client = useApiClient();
  return useQueries({
    queries: versions.map((v) => ({
      queryKey: kitKeys.recipe(kitId, v),
      queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/kits/${kitId}/recipes/${v}`, { schema: recipeSchema, signal }),
      staleTime: LOOKUP_STALE_MS,
    })),
    combine: (results) => ({ recipes: results.flatMap((r) => (r.data ? [r.data] : [])), pending: results.some((r) => r.isPending), failed: results.some((r) => r.isError) }),
  });
}

export interface KitVersion {
  kitId: string;
  version: number;
}

/**
 * The selling version of each kit whose latest version is a draft, with its economics: the
 * preview endpoint prices only the latest version, so the selling one is priced as a recipe.
 */
export function useSellingVersions(kits: readonly KitVersion[]) {
  const client = useApiClient();
  return useQueries({
    queries: kits.map((k) => ({
      queryKey: kitKeys.activeCost(k.kitId, k.version),
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const recipe = await client.get(`/kits/${k.kitId}/recipes/${k.version}`, { schema: recipeSchema, signal });
        const body: RecipeRequest = { items: recipe.items.map(itemRequestOf), ...(dec(recipe.packing_cost).gt(0) ? { packing_cost: recipe.packing_cost } : {}) };
        const cost = await client.post("/kits:preview", body, { schema: costViewSchema, signal });
        return { recipe, cost };
      },
      staleTime: DETAIL_STALE_MS,
    })),
    combine: (results) => new Map(results.flatMap((r, i) => (r.data ? [[(kits[i] as KitVersion).kitId, r.data] as const] : []))),
  });
}

/** One recipe version (the version an assembly order was planned on). */
export function useRecipe(kitId: string | null, version: number | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.recipe(kitId ?? "", version ?? 0),
    queryFn: ({ signal }) => client.get(`/kits/${kitId}/recipes/${version}`, { schema: recipeSchema, signal }),
    enabled: kitId !== null && version !== null,
    staleTime: LOOKUP_STALE_MS,
  });
}

/** Economics of an unsaved recipe (`POST /kits:preview`); null skips the request. */
export function useDraftCost(body: (RecipeRequest & { kit_price?: string }) | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.draftCost(body),
    queryFn: ({ signal }) => client.post("/kits:preview", body, { schema: costViewSchema, signal, silent: true }),
    enabled: body !== null,
    placeholderData: keepPreviousData,
    retry: false,
  });
}

/** Assembly progress of each kit with open orders. */
export function useKitProgress() {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.progress,
    queryFn: ({ signal }) => client.get("/kits/progress", { schema: listOf(kitProgressSchema), signal }),
    select: (r) => r.data,
  });
}

/** Lệnh đóng giỏ list, newest first; `q` matches a document number or a kit name. */
export function useAssemblyList(q = "", enabled = true) {
  return useCursorList({ queryKey: [...kitKeys.assemblies, q], path: "/assembly-orders", item: assemblyOrderSchema, params: { q: q || undefined }, limit: PAGE, enabled });
}

/**
 * The order and posting that produced a kit lot: the lot's genealogy names the order's document
 * number, which the order list resolves, so a lot of any age is found.
 */
export function useKitLotSource(lotId: string | null) {
  const client = useApiClient();
  const genealogy = useQuery({
    queryKey: kitKeys.genealogy(lotId ?? ""),
    queryFn: ({ signal }) => client.get(`/lots/${lotId}/genealogy`, { schema: genealogySchema, signal }),
    enabled: lotId !== null,
  });
  const docNo = genealogy.data?.components[0]?.order_doc_no ?? null;
  const found = useAssemblyList(docNo ?? "", docNo !== null);
  const orderId = found.items.find((a) => a.doc_no === docNo)?.id ?? null;
  const order = useAssembly(orderId);
  const posting = order.data?.postings.find((p) => p.output_lot_id === lotId) ?? null;
  const settled = genealogy.isSuccess && (docNo === null || (!found.isLoading && (orderId === null || order.isSuccess)));
  return {
    order: posting ? (order.data ?? null) : null,
    posting,
    pending: lotId !== null && !settled && !genealogy.isError && !found.error && !order.isError,
    failed: genealogy.isError || found.error !== null || order.isError,
  };
}

/** One assembly order with its items and postings. */
export function useAssembly(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.assembly(id ?? ""),
    queryFn: ({ signal }) => client.get(`/assembly-orders/${id}`, { schema: assemblyDetailSchema, signal }),
    enabled: id !== null,
  });
}

/** Items and postings of each listed order (the list carries none). */
export function useAssemblies(ids: readonly string[]) {
  const client = useApiClient();
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: kitKeys.assembly(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/assembly-orders/${id}`, { schema: assemblyDetailSchema, signal }),
      staleTime: DETAIL_STALE_MS,
    })),
    combine: (results) => new Map(results.flatMap((r) => (r.data ? [[r.data.id, r.data] as const] : []))),
  });
}

/** Components a planned order needs against the free stock (`POST /assembly-orders:preview`). */
export function useAssemblyPreview(body: AssemblyRequest | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.assemblyPreview(body),
    queryFn: ({ signal }) => client.post("/assembly-orders:preview", body, { schema: assemblyPreviewSchema, signal, silent: true }),
    enabled: body !== null,
    placeholderData: keepPreviousData,
    retry: false,
  });
}

/** Phiếu tháo giỏ, newest first; `q` matches the disassembly or order number, a lot number or the kit name. */
export function useDisassemblyList(q = "") {
  return useCursorList({ queryKey: [...kitKeys.disassemblies, q], path: "/disassemblies", item: disassemblySchema, params: { q: q || undefined }, limit: PAGE });
}

/** Lots of assembled kits holding stock (`GET /lots?kind=kit`). */
export function useKitLots() {
  return useCursorList({ queryKey: kitLotsKey, path: "/lots", item: lotSchema, params: { kind: "kit" }, limit: 200 });
}

/** Component lots of each kit lot. */
export function useGenealogies(lotIds: readonly string[]) {
  const client = useApiClient();
  return useQueries({
    queries: lotIds.map((id) => ({
      queryKey: kitKeys.genealogy(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/lots/${id}/genealogy`, { schema: genealogySchema, signal }),
    })),
    combine: (results) => new Map(results.flatMap((r) => (r.data ? [[r.data.lot_id, r.data] as const] : []))),
  });
}

/** Active mix groups, for "+ Thêm thùng mix vị". */
export function useMixGroups() {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.lookup("mix-groups"),
    queryFn: ({ signal }) => client.get("/mix-groups", { schema: listOf(mixGroupSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    // Every group, the stopped ones too: a saved recipe may still hold a line of one.
    select: (r) => r.data,
  });
}

const OPEN_INVOICE_STATUSES = ["confirmed", "partially_delivered"] as const;
const invoicePageSchema = z.object({ data: z.array(invoiceRefSchema) });

/** Confirmed invoices not fully delivered, for "Gắn đơn bán (tùy chọn)". */
export function useOpenInvoices(enabled: boolean) {
  const client = useApiClient();
  return useQuery({
    queryKey: kitKeys.lookup("open-invoices"),
    queryFn: async ({ signal }) => {
      const pages = await Promise.all(OPEN_INVOICE_STATUSES.map((status) => client.get("/invoices", { query: { status, limit: 100 }, schema: invoicePageSchema, signal })));
      return pages.flatMap((p) => p.data);
    },
    enabled,
    staleTime: 60_000,
  });
}
