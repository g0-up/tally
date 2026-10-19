import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import type { HttpClient } from "../../../shared/api/http";
import { isColorCode, type ColorCode, type ColorKey } from "../../../shared/domain/color";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import type { ComboItem } from "../../../shared/ui/combobox";
import { num, vnd } from "../../../shared/lib/format";
import {
  colorSchema,
  customerSchema,
  debtLedgerSchema,
  exchangeSchema,
  invoiceSchema,
  invoiceVersionsSchema,
  listOf,
  mixGroupSchema,
  paymentMethodSchema,
  printDocSchema,
  quoteHistorySchema,
  quoteSchema,
  reasonSchema,
  returnableLineSchema,
  salesDocumentDetailSchema,
  salesDocumentSchema,
  salesReturnSchema,
  searchItemSchema,
  skuDetailSchema,
  skuStockSchema,
  stockCardSchema,
  stockOutSchema,
  stockRowSchema,
  unitSchema,
  type Customer,
  type PrintDocType,
} from "../schemas";
import { rememberDocs, useRememberDocs } from "../doc-index";

// Read side of the sales screens. Every key starts with "sales" so a posting can refresh the
// whole feature with one prefix; lookups shared with POS live under "sales", "lookup".

export const salesKeys = {
  all: ["sales"] as const,
  lookup: (...rest: readonly unknown[]) => ["sales", "lookup", ...rest] as const,
  quotes: () => ["sales", "quotes"] as const,
  quote: (id: string) => ["sales", "quotes", "detail", id] as const,
  quoteHistory: (id: string) => ["sales", "quotes", "history", id] as const,
  invoices: () => ["sales", "invoices"] as const,
  invoice: (id: string) => ["sales", "invoices", "detail", id] as const,
  invoiceVersions: (id: string) => ["sales", "invoices", "versions", id] as const,
  invoiceStockOuts: (id: string) => ["sales", "invoices", "stock-outs", id] as const,
  stockOut: (id: string) => ["sales", "stock-outs", id] as const,
  stockOutLots: (id: string, skuId: string) => ["sales", "stock-outs", id, "lots", skuId] as const,
  documents: () => ["sales", "documents"] as const,
  document: (id: string) => ["sales", "documents", "detail", id] as const,
  returnable: (id: string) => ["sales", "documents", "returnable", id] as const,
  returns: () => ["sales", "returns"] as const,
  salesReturn: (id: string) => ["sales", "returns", "detail", id] as const,
  exchanges: () => ["sales", "exchanges"] as const,
  exchange: (id: string) => ["sales", "exchanges", "detail", id] as const,
  print: (type: PrintDocType, id: string) => ["sales", "print", type, id] as const,
};

const LOOKUP_STALE_MS = 5 * 60_000;
const LIST_LIMIT = 200;

/* ---------- lookups ---------- */

export interface ColorMaps {
  /** API color id → color code ("do"). */
  codeOf: (colorId: string | null | undefined) => ColorKey;
  /** Color code → API color id; null for "none" or an unknown code. */
  idOf: (color: ColorKey | null) => string | null;
}

/** The fixed product colors keyed both ways (API ids are per tenant, codes are fixed). */
export function useColorMaps(): ColorMaps {
  const client = useApiClient();
  const { data } = useQuery({
    queryKey: salesKeys.lookup("colors"),
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
      idOf: (color) => (color === null || color === "none" ? null : (byCode.get(color) ?? null)),
    };
  }, [data]);
}

const customerPageSchema = z.object({ data: z.array(customerSchema), next_cursor: z.string().nullish() });

/** Every customer, page after page, so a name is found however many customers the shop has. */
async function allCustomers(client: HttpClient, signal: AbortSignal): Promise<Customer[]> {
  const out: Customer[] = [];
  let cursor: string | null = null;
  do {
    const page: z.infer<typeof customerPageSchema> = await client.get("/customers", { query: { limit: LIST_LIMIT, cursor }, schema: customerPageSchema, signal });
    out.push(...page.data);
    cursor = page.next_cursor ?? null;
  } while (cursor !== null);
  return out;
}

/** Customers by id (names for the document lists; the API lists carry only the party id). */
export function useCustomerDirectory() {
  const client = useApiClient();
  const { data } = useQuery({
    queryKey: salesKeys.lookup("customers"),
    queryFn: ({ signal }) => allCustomers(client, signal),
    staleTime: LOOKUP_STALE_MS,
  });
  return useMemo(() => {
    const byId = new Map<string, Customer>((data ?? []).map((c) => [c.id, c]));
    return {
      get: (id: string | null | undefined) => (id ? byId.get(id) : undefined),
      name: (id: string | null | undefined) => (id ? (byId.get(id)?.name ?? "—") : WALK_IN_NAME),
    };
  }, [data]);
}

export const WALK_IN_NAME = "Khách lẻ";

export function useCustomer(id: string | null | undefined) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.lookup("customer", id),
    queryFn: ({ signal }) => client.get(`/customers/${id}`, { schema: customerSchema, signal }),
    enabled: Boolean(id),
    staleTime: 60_000,
  });
}

/** Receivable, advances and pending transfers of a party (`GET /debts/parties/{id}`). */
export function useDebtLedger(partyId: string | null | undefined) {
  const client = useApiClient();
  return useQuery({
    queryKey: ["debt", "party", partyId],
    queryFn: ({ signal }) => client.get(`/debts/parties/${partyId}`, { schema: debtLedgerSchema, signal, silent: true }),
    enabled: Boolean(partyId),
    staleTime: 30_000,
  });
}

/** Unit names by id (mix groups and lines carry only unit ids). */
export function useUnitNames() {
  const client = useApiClient();
  const { data } = useQuery({
    queryKey: salesKeys.lookup("units"),
    queryFn: ({ signal }) => client.get("/units", { schema: listOf(unitSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
  });
  return useMemo(() => {
    const byId = new Map((data?.data ?? []).map((u) => [u.id, u.name]));
    return (unitId: string | null | undefined) => (unitId ? (byId.get(unitId) ?? "") : "");
  }, [data]);
}

export function useReasons(kind: "sales_return" | "return_without_source") {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.lookup("reasons", kind),
    queryFn: ({ signal }) => client.get("/reasons", { query: { kind, active: true }, schema: listOf(reasonSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((x) => x.active),
  });
}

export function usePaymentMethods() {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.lookup("payment-methods"),
    queryFn: ({ signal }) => client.get("/payment-methods", { query: { limit: LIST_LIMIT }, schema: listOf(paymentMethodSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((m) => m.active),
  });
}

export function useMixGroups() {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.lookup("mix-groups"),
    queryFn: ({ signal }) => client.get("/mix-groups", { query: { status: "active", limit: LIST_LIMIT }, schema: listOf(mixGroupSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((g) => g.status === "active"),
  });
}

export function skuDetailQuery(client: HttpClient, skuId: string) {
  return {
    queryKey: salesKeys.lookup("sku", skuId),
    queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/skus/${skuId}`, { schema: skuDetailSchema, signal }),
    staleTime: LOOKUP_STALE_MS,
  };
}

export function useSkuDetail(skuId: string | null) {
  const client = useApiClient();
  return useQuery({ ...skuDetailQuery(client, skuId ?? ""), enabled: skuId !== null });
}

export function skuStockQuery(client: HttpClient, skuId: string) {
  return {
    queryKey: ["inventory", "stock", skuId] as const,
    queryFn: ({ signal }: { signal: AbortSignal }) => client.get(`/stock/${skuId}`, { schema: skuStockSchema, signal }),
    staleTime: 15_000,
  };
}

/** Sellable stock by color of one SKU (the color picker offers only colors that can be sold). */
export function useSkuStock(skuId: string | null) {
  const client = useApiClient();
  return useQuery({ ...skuStockQuery(client, skuId ?? ""), enabled: skuId !== null });
}

/* ---------- pickers ---------- */

const SEARCH_LIMIT = 30;

/** Sellable SKUs for the line editors: `/search` while typing, the stock list when empty. */
export function skuSearchSource(colors: ColorMaps) {
  return {
    key: "sales-sku",
    search: async ({ client, query, signal }: { client: HttpClient; query: string; signal: AbortSignal }): Promise<ComboItem[]> => {
      const q = query.trim();
      if (q === "") {
        const r = await client.get("/stock", { query: { limit: 50 }, schema: listOf(stockRowSchema), signal });
        return r.data.map((s) => ({
          id: s.sku_id,
          name: s.variant_label ? `${s.name} ${s.variant_label}` : s.name,
          inputLabel: `${s.code} · ${s.name}`,
          detail: `${s.code} · có thể bán ${num(s.available)} ${s.unit_name}${s.price !== null ? ` · ${vnd(s.price)}` : ""}`,
          colors: s.colors.flatMap((c) => (isColorCode(c.color_code) ? [c.color_code] : [])),
        }));
      }
      const r = await client.get("/search", { query: { type: "sku", q: q.slice(0, 100), limit: SEARCH_LIMIT }, schema: listOf(searchItemSchema), signal });
      return r.data.map((s) => ({
        id: s.id,
        name: s.label,
        inputLabel: `${s.code} · ${s.label}`,
        detail: [s.code, s.sublabel, s.price_hint !== null ? vnd(s.price_hint) : null].filter(Boolean).join(" · "),
        colors: s.on_hand_by_color.flatMap((c) => {
          const code = colors.codeOf(c.color_id);
          return code !== "none" ? [code] : [];
        }),
      }));
    },
  };
}

/** Customers for the document header pickers. */
export const customerSearchSource = {
  key: "sales-customer",
  search: async ({ client, query, signal }: { client: HttpClient; query: string; signal: AbortSignal }): Promise<ComboItem[]> => {
    const r = await client.get("/customers", { query: { q: query.trim(), status: "active", limit: 50 }, schema: listOf(customerSchema), signal });
    return r.data.map((c) => ({ id: c.id, name: c.name, detail: [c.phone, c.customer_group_name].filter(Boolean).join(" · "), keywords: c.phone ? [c.phone] : [] }));
  },
};

/* ---------- documents ---------- */

export function useQuoteList() {
  const list = useCursorList({ queryKey: salesKeys.quotes(), path: "/quotes", item: quoteSchema, limit: LIST_LIMIT });
  useRememberDocs("quote", list.items);
  return list;
}

export function useQuote(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.quote(id ?? ""),
    queryFn: async ({ signal }) => {
      const r = await client.request("GET", `/quotes/${id}`, { schema: quoteSchema, signal });
      rememberDocs("quote", [r.data]);
      return { ...r.data, etag: r.etag };
    },
    enabled: id !== null,
  });
}

export function useQuoteHistory(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.quoteHistory(id ?? ""),
    queryFn: ({ signal }) => client.get(`/quotes/${id}/history`, { schema: quoteHistorySchema, signal }),
    enabled: id !== null,
  });
}

export function useInvoiceList() {
  const list = useCursorList({ queryKey: salesKeys.invoices(), path: "/invoices", item: invoiceSchema, limit: LIST_LIMIT });
  useRememberDocs("invoice", list.items);
  return list;
}

export function invoiceQuery(client: HttpClient, id: string) {
  return {
    queryKey: salesKeys.invoice(id),
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const r = await client.request("GET", `/invoices/${id}`, { schema: invoiceSchema, signal });
      rememberDocs("invoice", [r.data]);
      return { ...r.data, etag: r.etag };
    },
  };
}

export function useInvoice(id: string | null) {
  const client = useApiClient();
  return useQuery({ ...invoiceQuery(client, id ?? ""), enabled: id !== null });
}

export function useInvoiceVersions(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.invoiceVersions(id ?? ""),
    queryFn: ({ signal }) => client.get(`/invoices/${id}/versions`, { schema: invoiceVersionsSchema, signal }),
    enabled: id !== null,
  });
}

/** Deliveries of an invoice: each stock-out posts one sales document carrying its stock-out id. */
export function useInvoiceDeliveries(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.invoiceStockOuts(id ?? ""),
    queryFn: async ({ signal }) => {
      const r = await client.get("/sales-documents", { query: { invoice_id: id, limit: LIST_LIMIT }, schema: listOf(salesDocumentSchema), signal });
      rememberDocs("document", r.data);
      return r;
    },
    enabled: id !== null,
    select: (r) => r.data,
  });
}

function stockOutQuery(client: HttpClient, id: string) {
  return {
    queryKey: salesKeys.stockOut(id),
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const r = await client.get(`/stock-outs/${id}`, { schema: stockOutSchema, signal });
      rememberDocs("stockOut", [r]);
      if (r.sales_document) rememberDocs("document", [r.sales_document]);
      return r;
    },
  };
}

export function useStockOut(id: string | null) {
  const client = useApiClient();
  return useQuery({ ...stockOutQuery(client, id ?? ""), enabled: id !== null });
}

/** Stock-outs of an invoice by id (the deliveries list carries only the stock-out id). */
export function useStockOuts(ids: readonly string[]) {
  const client = useApiClient();
  return useQueries({ queries: ids.map((id) => stockOutQuery(client, id)) });
}

/** Lots a stock-out took for one SKU: its own moves on the stock card of its business date. */
export function useStockOutLots(stockOutId: string, skuId: string, date: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.stockOutLots(stockOutId, skuId),
    queryFn: ({ signal }) => client.get("/stock-card", { query: { sku_id: skuId, from: date, to: date, limit: LIST_LIMIT }, schema: stockCardSchema, signal }),
    select: (r) => r.rows.filter((m) => m.doc_id === stockOutId),
  });
}

export function useSalesDocumentList() {
  const list = useCursorList({ queryKey: salesKeys.documents(), path: "/sales-documents", item: salesDocumentSchema, limit: LIST_LIMIT });
  useRememberDocs("document", list.items);
  return list;
}

export function useSalesDocument(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.document(id ?? ""),
    queryFn: async ({ signal }) => {
      const r = await client.get(`/sales-documents/${id}`, { schema: salesDocumentDetailSchema, signal });
      rememberDocs("document", [r]);
      return r;
    },
    enabled: id !== null,
  });
}

export function useReturnableLines(salesDocumentId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.returnable(salesDocumentId ?? ""),
    queryFn: ({ signal }) => client.get(`/sales-documents/${salesDocumentId}/returnable-lines`, { schema: listOf(returnableLineSchema), signal }),
    enabled: salesDocumentId !== null,
    select: (r) => r.data,
  });
}

export function useSalesReturnList() {
  const list = useCursorList({ queryKey: salesKeys.returns(), path: "/sales-returns", item: salesReturnSchema, limit: LIST_LIMIT });
  useRememberDocs("return", list.items);
  return list;
}

export function useSalesReturn(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.salesReturn(id ?? ""),
    queryFn: async ({ signal }) => {
      const r = await client.get(`/sales-returns/${id}`, { schema: salesReturnSchema, signal });
      rememberDocs("return", [r]);
      return r;
    },
    enabled: id !== null,
  });
}

export function useExchangeList() {
  const list = useCursorList({ queryKey: salesKeys.exchanges(), path: "/exchanges", item: exchangeSchema, limit: LIST_LIMIT });
  useRememberDocs("exchange", list.items);
  return list;
}

export function useExchange(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.exchange(id ?? ""),
    queryFn: async ({ signal }) => {
      const r = await client.get(`/exchanges/${id}`, { schema: exchangeSchema, signal });
      rememberDocs("exchange", [r]);
      return r;
    },
    enabled: id !== null,
  });
}

export function usePrintDoc(type: PrintDocType, id: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: salesKeys.print(type, id),
    queryFn: ({ signal }) => client.get(`/print/${type}/${id}`, { schema: printDocSchema, signal }),
  });
}
