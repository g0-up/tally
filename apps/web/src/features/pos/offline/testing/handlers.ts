import { HttpResponse, http } from "msw";
import { PATHS } from "../../../../shared/api/endpoints";
import { toMoney } from "../../../../shared/domain/money";
import { TEST_ME, TEST_TENANT, apiError, apiUrl } from "../../../../test/msw/handlers";
import { posTileSchema } from "../../../sales/schemas";
import type { CheckoutIntent, OfflineBundle, OutboxEntry } from "../offline-db";
import type { NewOutboxEntry } from "../outbox-repository";

// MSW handlers and fixtures of the offline POS endpoints, applied per test with `server.use(...)`:
// the store sells offline with a 2 000 000 đ cap and 12 hours, one cash and one transfer method,
// two products, the second with a box and a carton barcode. Each handler records what it received
// so tests can assert on the requests.

export const OFFLINE_TENANT_ID = TEST_TENANT.id;
export const OFFLINE_SESSION_ID = "01J00000000000000000SESSION";
export const CASH_METHOD_ID = "pm-cash";
export const BANK_METHOD_ID = "pm-bank";

/** GET /me as the POS reads it: the shell's answer plus the login session. */
export function meWithSession(isSandbox = false) {
  const { id, slug, display_name } = TEST_TENANT;
  return { ...TEST_ME, tenant: { id, slug, display_name, is_sandbox: isSandbox }, session: { id: OFFLINE_SESSION_ID } };
}

export const OFFLINE_SETTINGS = { prices_include_tax: true, offline_pos_enabled: true, offline_pos_max_amount: "2000000", offline_pos_max_hours: 12 };

export const OFFLINE_METHODS = [
  { id: CASH_METHOD_ID, code: "TM", name: "Tiền mặt", fund_account_id: "fa-cash", fund_kind: "cash" as const, cash: true, active: true },
  { id: BANK_METHOD_ID, code: "CK", name: "Chuyển khoản", fund_account_id: "fa-bank", fund_kind: "bank" as const, cash: false, active: true },
];

export const OFFLINE_TILES = [
  { sku_id: "sku-keo", sku_code: "KEO-DUA", name: "Kẹo dừa", variant_label: "", unit_id: "unit-goi", unit_name: "Gói", retail_price: "50000", stock: [{ color_id: null, qty: "40" }], barcodes: [] },
  {
    sku_id: "sku-mut",
    sku_code: "MUT-GUNG",
    name: "Mứt gừng",
    variant_label: "",
    unit_id: "unit-hop",
    unit_name: "Hộp",
    retail_price: "120000",
    stock: [{ color_id: null, qty: "8" }],
    // One barcode on the box, one on the carton of 24 boxes.
    barcodes: [
      { code: "8935000000024", unit_id: "unit-hop", unit_name: "Hộp" },
      { code: "18935000000021", unit_id: "unit-thung", unit_name: "Thùng" },
    ],
  },
];

export const OFFLINE_COLORS = [{ id: "c-do", code: "do", name: "Đỏ", sort_order: 1, active: true }];

export const PRICED_AT = "2026-10-18T08:00:00.000Z";

/** A saved catalog of the test session; `now` decides its expiry window. */
export function testBundle(init: Partial<OfflineBundle> = {}): OfflineBundle {
  return {
    scope: `${OFFLINE_TENANT_ID}:${OFFLINE_SESSION_ID}`,
    tenant_id: OFFLINE_TENANT_ID,
    device_session_id: OFFLINE_SESSION_ID,
    version: '"boot-1"',
    cached_at: PRICED_AT,
    fetched_at: PRICED_AT,
    expires_at: "2026-10-18T20:00:00.000Z",
    settings: OFFLINE_SETTINGS,
    payment_methods: OFFLINE_METHODS,
    tiles: OFFLINE_TILES.map((t) => posTileSchema.parse(t)),
    colors: OFFLINE_COLORS,
    ...init,
  };
}

/** An outbox row as recordOfflineSale stores it: one line of Kẹo dừa paid in cash. */
export function testOutboxEntry(seq: number, init: Partial<OutboxEntry> = {}): OutboxEntry {
  const id = `0192a000-0000-7000-8000-${String(seq).padStart(12, "0")}`;
  return {
    offline_client_id: id,
    tenant_id: OFFLINE_TENANT_ID,
    device_session_id: OFFLINE_SESSION_ID,
    label: `OFF-${String(seq).padStart(3, "0")}`,
    seq,
    sale: {
      offline_client_id: id,
      created_at: "2026-10-18T09:00:00.000Z",
      lines: [{ sku_id: "sku-keo", unit_id: "unit-goi", qty: "2", unit_price: "50000" }],
      payments: [{ payment_method_id: CASH_METHOD_ID, amount: "100000" }],
      cash_tendered_amount: "100000",
    },
    total: toMoney("100000"),
    bundle_version: '"boot-1"',
    lines: [{ name: "Kẹo dừa", qty: "2", unit_name: "Gói", amount: toMoney("100000") }],
    status: "pending",
    ...init,
  };
}

/** The same sale as it is handed to addToOutbox, before the device numbers it. */
export function newOutboxEntry(seq: number, init: Partial<NewOutboxEntry> = {}): NewOutboxEntry {
  const { label: _label, seq: _seq, status: _status, exception: _exception, failures: _failures, ...entry } = testOutboxEntry(seq);
  return { ...entry, ...init };
}

/** An online cash checkout of 100 000 đ whose answer never arrived. */
export function testCheckoutIntent(n: number, init: Partial<CheckoutIntent> = {}): CheckoutIntent {
  return {
    idempotency_key: `0192b000-0000-7000-8000-${String(n).padStart(12, "0")}`,
    tenant_id: OFFLINE_TENANT_ID,
    path: "/pos/checkout",
    body: { lines: [{ sku_id: "sku-keo", unit_id: "unit-goi", qty: "2", unit_price: "50000" }], payments: [{ payment_method_id: CASH_METHOD_ID, amount: "100000" }] },
    if_match: null,
    total: toMoney("100000"),
    created_at: `2026-10-18T09:00:0${n}.000Z`,
    ...init,
  };
}

/** An open exception of the server queue for an outbox row. */
export function testSyncException(entry: OutboxEntry, init: { id?: string; error_code?: string; error_detail?: string | null; status?: "open" | "resolved" | "discarded" } = {}) {
  return {
    id: init.id ?? `sx-${entry.seq}`,
    offline_client_id: entry.offline_client_id,
    device_session_id: entry.device_session_id,
    payload: entry.sale,
    error_code: init.error_code ?? "sku_inactive",
    error_detail: init.error_detail ?? "sku is inactive",
    status: init.status ?? "open",
    created_at: "2026-10-18T10:00:00.000Z",
  };
}

/** POST :resolve answer: the posted offline sale. */
export function testPosSale(docNo: string) {
  const totals = { goods_amount: "100000", discount_amount: "0", tax_amount: "0", total_amount: "100000" };
  return {
    sales_document: {
      id: `sd-${docNo}`,
      doc_no: docNo,
      source: "pos_offline",
      stock_out_id: null,
      invoice_id: null,
      pos_cart_id: null,
      customer_party_id: null,
      payer_party_id: null,
      business_date: "2026-10-18",
      status: "posted",
      posted_at: "2026-10-18T10:05:00Z",
      cash_tendered_amount: "100000",
      change_amount: "0",
      ...totals,
      payment: { total_amount: "100000", paid_amount: "100000", open_amount: "0", pending_amount: "0", code: "paid", label: "Đã thu đủ" },
    },
    shift: { id: "shift-1", doc_no: "CA-0001", business_date: "2026-10-18", opened_at: "2026-10-18T07:00:00Z", closed_at: null, status: "open" },
    payments: [{ id: "pay-1", doc_no: "PT-0001", status: "posted", amount: "100000" }],
  };
}

export type SyncItemStatus = "posted" | "replayed" | "exception" | "failed";
export interface SyncItemAnswer {
  status: SyncItemStatus;
  doc_no?: string | null;
  sync_exception_id?: string | null;
  error_code?: string | null;
  error_detail?: string | null;
}

/**
 * A server that posts each offline sale once by offline_client_id: the first upload answers
 * `answer(id)` (posted by default), any later upload of the same id "replayed".
 */
export function offlineSyncServer(answer: (offlineClientId: string, call: number) => SyncItemAnswer = () => ({ status: "posted" })) {
  const uploads: string[] = [];
  const posted = new Set<string>();
  let docs = 0;
  const handler = http.post(apiUrl("/pos/offline-sync"), async ({ request }) => {
    const body = (await request.json()) as { sales: { offline_client_id: string }[] };
    const items = body.sales.map((s) => {
      uploads.push(s.offline_client_id);
      if (posted.has(s.offline_client_id)) return { offline_client_id: s.offline_client_id, status: "replayed" };
      const a = answer(s.offline_client_id, uploads.length);
      if (a.status === "posted") {
        posted.add(s.offline_client_id);
        docs++;
        return { offline_client_id: s.offline_client_id, doc_no: `BH-OFF-${String(docs).padStart(4, "0")}`, ...a };
      }
      if (a.status === "exception") posted.add(s.offline_client_id);
      return { offline_client_id: s.offline_client_id, ...a };
    });
    return HttpResponse.json({ items });
  });
  return { handler, uploads, posted };
}

/** POST /pos/offline-session granting a window until `until`. */
export function offlineSessionHandler(until = "2026-10-19T08:00:00.000Z") {
  return http.post(apiUrl("/pos/offline-session"), () => HttpResponse.json({ offline_until: until }));
}

/** GET /pos/bootstrap with the store's offline settings, and GET /colors. */
export function bootstrapHandlers(settings: Partial<typeof OFFLINE_SETTINGS> = {}, etag = '"boot-1"') {
  return [
    http.get(apiUrl("/pos/bootstrap"), () =>
      HttpResponse.json({ payment_methods: OFFLINE_METHODS, shift: null, tiles: OFFLINE_TILES, priced_at: PRICED_AT, settings: { ...OFFLINE_SETTINGS, ...settings } }, { headers: { ETag: etag } }),
    ),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: OFFLINE_COLORS })),
  ];
}

/** GET /me with the login session (`is_sandbox` for the simulation toggle). */
export function meHandler(isSandbox = false) {
  return http.get(apiUrl(PATHS.me), () => HttpResponse.json(meWithSession(isSandbox)));
}

/** GET /pos/sync-exceptions?status=open answering `items` in one page. */
export function syncExceptionsHandler(items: () => unknown[]) {
  return http.get(apiUrl("/pos/sync-exceptions"), () => HttpResponse.json({ data: items(), next_cursor: null }));
}

export interface ResolveCall {
  id: string;
  body: unknown;
  idempotencyKey: string | null;
}

/** POST /pos/sync-exceptions/{id}:resolve and :discard; `refuse` answers an error instead. */
export function exceptionActionHandlers(opts: { refuse?: (action: "resolve" | "discard", id: string) => Response | null; docNo?: string } = {}) {
  const resolved: ResolveCall[] = [];
  const discarded: string[] = [];
  const handler = http.post(apiUrl("/pos/sync-exceptions/:action"), async ({ params, request }) => {
    const raw = String(params.action);
    const [id = "", action = ""] = raw.split(":");
    if (action !== "resolve" && action !== "discard") return apiError(404, "not_found");
    const refused = opts.refuse?.(action, id) ?? null;
    if (refused) return refused;
    if (action === "resolve") {
      resolved.push({ id, body: await request.json(), idempotencyKey: request.headers.get("Idempotency-Key") });
      return HttpResponse.json(testPosSale(opts.docNo ?? "BH-0042"), { status: 201 });
    }
    discarded.push(id);
    return HttpResponse.json({ id, offline_client_id: "", device_session_id: null, payload: {}, error_code: "sku_inactive", error_detail: null, status: "discarded", created_at: "2026-10-18T10:00:00.000Z" });
  });
  return { handler, resolved, discarded };
}

export const TENANT_SETTINGS = { offline_pos_enabled: false, offline_pos_max_amount: "2000000", offline_pos_max_hours: 12, updated_at: "2026-10-18T07:00:00Z", version: 3 };

export interface SettingsPatchCall {
  body: Record<string, unknown>;
  ifMatch: string | null;
}

/** GET/PATCH /tenant/settings; a PATCH bumps the version and answers the saved settings. */
export function tenantSettingsHandlers(initial: typeof TENANT_SETTINGS = TENANT_SETTINGS) {
  let current = { ...initial };
  const patches: SettingsPatchCall[] = [];
  const handlers = [
    http.get(apiUrl("/tenant/settings"), () => HttpResponse.json(current)),
    http.patch(apiUrl("/tenant/settings"), async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>;
      patches.push({ body, ifMatch: request.headers.get("If-Match") });
      current = { ...current, ...body, version: current.version + 1 };
      return HttpResponse.json(current);
    }),
  ];
  return { handlers, patches };
}
