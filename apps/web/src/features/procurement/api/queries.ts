import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import {
  lotSchema,
  orderLineSchema,
  orderSchema,
  partyLedgerSchema,
  paymentMethodSchema,
  purchaseReturnSchema,
  reasonSchema,
  receiptListItemSchema,
  receiptSchema,
  supplierRefSchema,
} from "../schemas";

// Read side of the Nhập hàng screens. Every key starts with "procurement" so a posting refreshes
// orders, receipts and returns with one prefix.

export const procurementKeys = {
  all: ["procurement"] as const,
  orders: (q: string) => ["procurement", "orders", "list", q] as const,
  order: (id: string) => ["procurement", "orders", "detail", id] as const,
  orderByDoc: (docNo: string) => ["procurement", "orders", "by-doc", docNo] as const,
  receivable: (orderId: string) => ["procurement", "orders", "receivable", orderId] as const,
  openOrders: ["procurement", "orders", "open"] as const,
  orderReceipts: (orderId: string) => ["procurement", "receipts", "by-order", orderId] as const,
  receipts: (q: string) => ["procurement", "receipts", "list", q] as const,
  receipt: (id: string) => ["procurement", "receipts", "detail", id] as const,
  receiptByDoc: (docNo: string) => ["procurement", "receipts", "by-doc", docNo] as const,
  returns: ["procurement", "returns", "list"] as const,
  returnByDoc: (docNo: string) => ["procurement", "returns", "by-doc", docNo] as const,
  lookup: (...rest: readonly unknown[]) => ["procurement", "lookup", ...rest] as const,
};

/** Debt screens read payables and advances under this prefix; postings here change both. */
export const DEBT_KEY = ["debt"] as const;

const LOOKUP_STALE_MS = 5 * 60_000;
/** Rows one list page holds. */
const PAGE = 50;
/** Most rows the doc-number lookups and the "Nhận theo …" buttons read. */
const LOOKUP_LIMIT = 50;

/** Đơn nhập list (`GET /purchase-orders?q=`), newest first. */
export function useOrderList(q: string) {
  return useCursorList({ queryKey: procurementKeys.orders(q), path: "/purchase-orders", item: orderSchema, params: { q }, limit: PAGE });
}

/** Open orders (confirmed or partly received): one "Nhận theo …" button each on Nhận hàng. */
export function useOpenOrders() {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.openOrders,
    queryFn: async ({ signal }) => {
      const [confirmed, partial] = await Promise.all(
        ["confirmed", "partially_received"].map((status) => client.get("/purchase-orders", { query: { status, limit: LOOKUP_LIMIT }, schema: listOf(orderSchema), signal })),
      );
      return [...(confirmed?.data ?? []), ...(partial?.data ?? [])].sort((a, b) => a.doc_no.localeCompare(b.doc_no));
    },
  });
}

/** One PO with its lines. */
export function useOrder(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.order(id ?? ""),
    queryFn: ({ signal }) => client.get(`/purchase-orders/${id}`, { schema: orderSchema, signal }),
    enabled: id !== null,
  });
}

/** PO lines that still wait for goods, with their pending base quantity. */
export function useReceivableLines(orderId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.receivable(orderId ?? ""),
    queryFn: ({ signal }) => client.get(`/purchase-orders/${orderId}/receivable-lines`, { schema: listOf(orderLineSchema), signal }),
    enabled: orderId !== null,
    select: (r) => r.data,
  });
}

/** Receipts posted against a PO, for the PO drawer. */
export function useOrderReceipts(orderId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.orderReceipts(orderId ?? ""),
    queryFn: ({ signal }) => client.get("/receipts", { query: { purchase_order_id: orderId, limit: LOOKUP_LIMIT }, schema: listOf(receiptListItemSchema), signal }),
    enabled: orderId !== null,
    select: (r) => r.data,
  });
}

/** Phiếu nhận list (`GET /receipts?q=`), newest first. */
export function useReceiptList(q: string) {
  return useCursorList({ queryKey: procurementKeys.receipts(q), path: "/receipts", item: receiptListItemSchema, params: { q }, limit: PAGE });
}

/** One receipt with lines, payable, settlements and attachments. */
export function useReceipt(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.receipt(id ?? ""),
    queryFn: ({ signal }) => client.get(`/receipts/${id}`, { schema: receiptSchema, signal }),
    enabled: id !== null,
  });
}

/** Trả NCC list (`GET /purchase-returns`), newest first. */
export function useReturnList() {
  return useCursorList({ queryKey: procurementKeys.returns, path: "/purchase-returns", item: purchaseReturnSchema, limit: PAGE });
}

const postedRef = orderSchema.pick({ id: true, doc_no: true });

/**
 * Id of the document with exactly this number, found through the list search (`?q=`). The doc-no
 * links of other screens arrive with the number only.
 */
function useIdByDoc(path: string, key: readonly unknown[], docNo: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      const r = await client.get(path, { query: { q: docNo, limit: LOOKUP_LIMIT }, schema: listOf(postedRef), signal });
      return r.data.find((d) => d.doc_no === docNo)?.id ?? null;
    },
    enabled: docNo !== null,
  });
}

export const useOrderIdByDoc = (docNo: string | null) => useIdByDoc("/purchase-orders", procurementKeys.orderByDoc(docNo ?? ""), docNo);
export const useReceiptIdByDoc = (docNo: string | null) => useIdByDoc("/receipts", procurementKeys.receiptByDoc(docNo ?? ""), docNo);

/** A return found by its number in the list search. */
export function useReturnByDoc(docNo: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.returnByDoc(docNo ?? ""),
    queryFn: async ({ signal }) => {
      const r = await client.get("/purchase-returns", { query: { q: docNo, limit: LOOKUP_LIMIT }, schema: listOf(purchaseReturnSchema), signal });
      return r.data.find((d) => d.doc_no === docNo) ?? null;
    },
    enabled: docNo !== null,
  });
}

/** Active reasons of a kind ("close_short", "purchase_return"). */
export function useReasons(kind: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.lookup("reasons", kind),
    queryFn: ({ signal }) => client.get("/reasons", { query: { kind, active: true }, schema: listOf(reasonSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((x) => x.active),
  });
}

/** Active payment methods ("Chi từ quỹ"). */
export function usePaymentMethods() {
  const client = useApiClient();
  return useQuery({
    queryKey: procurementKeys.lookup("payment-methods"),
    queryFn: ({ signal }) => client.get("/payment-methods", { schema: listOf(paymentMethodSchema), signal }),
    staleTime: LOOKUP_STALE_MS,
    select: (r) => r.data.filter((m) => m.active),
  });
}

/** Advances paid to a supplier (`GET /debts/parties/{id}`), for the PO drawer. */
export function usePartyAdvances(partyId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: [...DEBT_KEY, "parties", partyId ?? "", "advances"],
    queryFn: ({ signal }) => client.get(`/debts/parties/${partyId}`, { schema: partyLedgerSchema, signal }),
    enabled: partyId !== null,
    select: (r) => r.advances ?? [],
  });
}

/** Lots of a SKU in stock, for "Tồn lô tại kho". */
export function useSkuLots(skuIds: readonly string[]) {
  const client = useApiClient();
  const ids = [...new Set(skuIds)].sort();
  return useQuery({
    queryKey: ["inventory", "lots", "by-sku", ...ids],
    queryFn: async ({ signal }) => {
      const pages = await Promise.all(ids.map((sku_id) => client.get("/lots", { query: { sku_id, limit: 200 }, schema: listOf(lotSchema), signal })));
      return pages.flatMap((p) => p.data);
    },
    enabled: ids.length > 0,
  });
}

/** A supplier's name, for a PO opened from Nhà cung cấp with `?supplier=`. */
export function useSupplierRef(id: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: ["partners", "suppliers", "ref", id ?? ""],
    queryFn: ({ signal }) => client.get(`/suppliers/${id}`, { schema: supplierRefSchema, signal }),
    enabled: id !== null,
    staleTime: LOOKUP_STALE_MS,
  });
}
