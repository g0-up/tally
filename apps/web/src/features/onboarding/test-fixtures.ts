import { HttpResponse, http } from "msw";
import { ONBOARDING_STEP_KEYS, PATHS, type OnboardingStepKey, type OnboardingStepStatus, type Tenant } from "../../shared/api/endpoints";
import { TEST_TENANT, apiError, apiUrl, type OnboardingBody } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";

// MSW answers of the onboarding screen in the API's real shape, with a recorder for the writes.
// `installOnboardingApi` installs a small stateful API: step commands and postings move the checklist
// the way the server does, so a test reads the screen after each write.

export const CUTOFF = "2026-09-30";
export const COLOR_DO = "clr-do";
export const CUSTOMER_ID = "cus-1";
/** The customer the quick add creates. */
export const NEW_CUSTOMER_ID = "cus-new";
export const DEBT_REASON_ID = "rsn-opening";

/** The store profile (GET /tenant), cut off on CUTOFF. */
export const TEST_PROFILE: Tenant = { ...TEST_TENANT, opening_cutoff_date: CUTOFF };

/** Opening totals at the cutoff; every amount has thousands so the formatting shows. */
export const TEST_TOTALS = {
  cutoff: CUTOFF,
  stock_by_color: [
    { color_id: COLOR_DO, color_code: "do", color_name: "Đỏ", qty: "24", value: "1250000" },
    { color_id: null, color_code: null, color_name: null, qty: "10", value: "90000" },
  ],
  stock_value: "1340000",
  receivables: "2500000",
  payables: "750000",
  customer_advances: "0",
  supplier_advances: "120000",
  funds: [
    { id: "fund-cash", name: "Tiền mặt tại cửa hàng", kind: "cash", balance: "5000000" },
    { id: "fund-bank", name: "Tài khoản ngân hàng", kind: "bank", balance: "12345678" },
  ],
};

export function testFund(over: Record<string, unknown> = {}) {
  return { id: "fund-cash", kind: "cash", name: "Tiền mặt tại cửa hàng", opening_balance: "0", opening_date: null, balance: "0", active: true, version: 1, ...over };
}

export const TEST_FUNDS = [testFund(), testFund({ id: "fund-bank", kind: "bank", name: "Tài khoản ngân hàng", version: 2 })];

export const TEST_REASONS = [
  { id: "rsn-adjust", kind: "debt_adjustment", name: "Điều chỉnh công nợ", active: true },
  { id: DEBT_REASON_ID, kind: "debt_adjustment", name: "Số dư đầu kỳ", active: true },
];

export const TEST_COLORS = [{ id: COLOR_DO, code: "do", name: "Đỏ", active: true }];

export const TEST_CUSTOMERS = [{ id: CUSTOMER_ID, name: "Tạp hóa Cô Lan", phone: "0912000111", customer_group_name: null }];

/** A goods SKU of the picker; lot tracking on request. */
export function testSkuItem(over: Record<string, unknown> = {}) {
  return {
    id: "sku-1",
    product_id: "prod-1",
    code: "SP001",
    display_name: "Bánh quy bơ",
    variant_label: "",
    base_unit_id: "unit-hop",
    track_lot: false,
    track_expiry: false,
    ref_cost: "45000",
    min_price: null,
    min_stock: "0",
    spec_text: null,
    status: "active",
    product_type: "goods",
    category_id: null,
    category_name: null,
    units: [{ unit_id: "unit-hop", unit_name: "hộp", factor: "1", is_base: true, active: true, is_default_sale: true, is_default_purchase: true }],
    barcode: null,
    retail_price: "60000",
    wholesale_price: null,
    has_color_prices: false,
    on_hand: "0",
    sellable: "0",
    avg_cost: null,
    last_purchase_price: null,
    in_mix_group: false,
    on_hand_by_color: [],
    ...over,
  };
}

/** A line of a posted opening stock document (`GET /stock-adjustments/{id}`). */
export function testStockLine(over: Record<string, unknown> = {}) {
  return { id: "adj-line-1", sku_id: "sku-1", sku_code: "SP001", name: "Bánh quy bơ", variant_label: "", qty: "10", color_id: null, lot_no: null, expiry_date: null, unit_cost: "45000", ...over };
}

const SKIPPABLE: ReadonlySet<OnboardingStepKey> = new Set(["catalog_setup", "first_sale"]);

export type StepStatuses = Partial<Record<OnboardingStepKey, OnboardingStepStatus>>;

/** `GET /onboarding` in the real shape: every step pending unless given. */
export function testStatus(statuses: StepStatuses = {}, extra: { totals?: typeof TEST_TOTALS | null; locked_through?: string | null } = {}): OnboardingBody {
  const steps = ONBOARDING_STEP_KEYS.map((key) => {
    const status = statuses[key] ?? "pending";
    return {
      key,
      status,
      skippable: SKIPPABLE.has(key),
      completed_at: status === "completed" ? "2026-10-01T03:00:00Z" : null,
      skipped_at: status === "skipped" ? "2026-10-01T03:00:00Z" : null,
    };
  });
  const complete = steps.every((s) => s.status === "completed" || (s.skippable && s.status === "skipped"));
  const done = steps.filter((s) => s.status !== "pending").length;
  return { steps, done, total: steps.length, complete, locked_through: extra.locked_through ?? null, totals: extra.totals === undefined ? TEST_TOTALS : extra.totals };
}

/** One write the screen sent, with the headers the tests check. */
export interface RecordedCall {
  method: string;
  path: string;
  body: unknown;
  idempotencyKey: string | null;
  ifMatch: string | null;
}

async function record(calls: RecordedCall[], request: Request): Promise<RecordedCall> {
  const text = await request.text();
  const call: RecordedCall = {
    method: request.method,
    path: new URL(request.url).pathname.replace(/^\/api\/v1/, ""),
    body: text === "" ? undefined : (JSON.parse(text) as unknown),
    idempotencyKey: request.headers.get("Idempotency-Key"),
    ifMatch: request.headers.get("If-Match"),
  };
  calls.push(call);
  return call;
}

/** The step a posting completes on the server, like the API's own completion hooks. */
const COMPLETES: Readonly<Record<string, OnboardingStepKey>> = {
  "/stock-adjustments": "opening_stock",
  "/debt-adjustments": "opening_debt",
};

export interface OnboardingApiOptions {
  steps?: StepStatuses;
  totals?: typeof TEST_TOTALS | null;
  profile?: Tenant;
  funds?: ReturnType<typeof testFund>[];
  skus?: ReturnType<typeof testSkuItem>[];
  /** Opening stock documents already posted. */
  stockDocs?: { id: string; reversal_of?: string; lines: object[] }[];
}

/** Installs the onboarding API for one test and returns what it recorded. */
export function installOnboardingApi(options: OnboardingApiOptions = {}) {
  const statuses: StepStatuses = { ...options.steps };
  let profile = options.profile ?? TEST_PROFILE;
  let funds = options.funds ?? TEST_FUNDS;
  const calls: RecordedCall[] = [];
  // Posted opening documents, read back by the step's "posted" tables.
  const stockDocs = [...(options.stockDocs ?? [])];
  const debtDocs: { id: string; kind: string; detail: object }[] = [];
  const customers = [...TEST_CUSTOMERS];
  const status = () => testStatus(statuses, { totals: options.totals === undefined ? TEST_TOTALS : options.totals, locked_through: statuses.confirm_opening_balances === "completed" ? CUTOFF : null });
  const stepView = (key: OnboardingStepKey) => {
    const s = status().steps.find((x) => x.key === key);
    return key === "confirm_opening_balances" ? { ...s, totals: TEST_TOTALS } : s;
  };

  server.use(
    http.get(apiUrl(PATHS.onboarding), () => HttpResponse.json(status())),
    http.post(apiUrl("/onboarding/steps/*"), async ({ request }) => {
      const call = await record(calls, request);
      const [key, action] = (call.path.split("/").pop() ?? "").split(":") as [OnboardingStepKey, string];
      if (action === "skip" && !SKIPPABLE.has(key)) return apiError(409, "step_not_skippable");
      if (action === "complete" && key === "first_sale") return apiError(409, "step_not_manual");
      statuses[key] = action === "skip" ? "skipped" : "completed";
      return HttpResponse.json(stepView(key));
    }),
    http.get(apiUrl(PATHS.tenant), () => HttpResponse.json(profile)),
    http.patch(apiUrl(PATHS.tenant), async ({ request }) => {
      const call = await record(calls, request);
      profile = { ...profile, ...(call.body as Partial<Tenant>), version: profile.version + 1 };
      return HttpResponse.json(profile);
    }),
    http.get(apiUrl("/funds"), () => HttpResponse.json({ data: funds })),
    http.patch(apiUrl("/funds/:id"), async ({ request, params }) => {
      const call = await record(calls, request);
      funds = funds.map((f) => (f.id === params.id ? { ...f, ...(call.body as object), version: f.version + 1 } : f));
      statuses.opening_cash = "completed";
      return HttpResponse.json(funds.find((f) => f.id === params.id));
    }),
    http.get(apiUrl("/reasons"), () => HttpResponse.json({ data: TEST_REASONS })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: TEST_COLORS })),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: options.skus ?? [testSkuItem()], next_cursor: null })),
    http.get(apiUrl("/customers"), () => HttpResponse.json({ data: TEST_CUSTOMERS })),
    http.get(apiUrl("/stock-adjustments"), () => HttpResponse.json({ data: stockDocs.map((d) => ({ id: d.id, reversal_of: d.reversal_of ?? null })), next_cursor: null })),
    http.get(apiUrl("/stock-adjustments/:id"), ({ params }) => HttpResponse.json(stockDocs.find((d) => d.id === params.id))),
    http.post(apiUrl("/stock-adjustments"), async ({ request }) => {
      const call = await record(calls, request);
      const key = COMPLETES[call.path];
      if (key) statuses[key] = "completed";
      const id = `adj-${stockDocs.length + 1}`;
      const body = call.body as { lines: { sku_id: string; qty: string; unit_cost: string; color_id?: string; lot_no?: string; expiry_date?: string }[] };
      const lines = body.lines.map((l, i) => {
        const sku = (options.skus ?? [testSkuItem()]).find((s) => s.id === l.sku_id);
        return { id: `${id}-${i}`, sku_id: l.sku_id, sku_code: sku?.code ?? "", name: sku?.display_name ?? "", variant_label: "", qty: l.qty, color_id: l.color_id ?? null, lot_no: l.lot_no ?? null, expiry_date: l.expiry_date ?? null, unit_cost: l.unit_cost };
      });
      stockDocs.push({ id, lines });
      return HttpResponse.json({ id, doc_no: `DC-000${stockDocs.length}`, kind: "opening", business_date: CUTOFF, status: "posted" }, { status: 201 });
    }),
    http.get(apiUrl("/debt-adjustments"), () => HttpResponse.json({ data: debtDocs.map((d) => ({ id: d.id, kind: d.kind })), next_cursor: null })),
    http.get(apiUrl("/debt-adjustments/:id"), ({ params }) => HttpResponse.json(debtDocs.find((d) => d.id === params.id)?.detail)),
    http.post(apiUrl("/debt-adjustments"), async ({ request }) => {
      const call = await record(calls, request);
      const key = COMPLETES[call.path];
      if (key) statuses[key] = "completed";
      const body = call.body as { party_id: string; direction: string; kind: string; amount: string; reason_id: string; due_date?: string; note?: string };
      const id = `dadj-${debtDocs.length + 1}`;
      const doc_no = `DCN-000${debtDocs.length + 1}`;
      debtDocs.push({
        id,
        kind: body.kind,
        detail: {
          id,
          doc_no,
          direction: body.direction,
          amount: body.amount,
          note: body.note ?? null,
          party_name: customers.find((c) => c.id === body.party_id)?.name ?? body.party_id,
          reason_name: TEST_REASONS.find((r) => r.id === body.reason_id)?.name ?? "",
          obligation: { due_date: body.due_date ?? null },
        },
      });
      return HttpResponse.json({ id, doc_no }, { status: 201 });
    }),
    http.post(apiUrl("/customers"), async ({ request }) => {
      const call = await record(calls, request);
      const body = call.body as { name: string; phone: string | null; payment_terms_days: number };
      customers.push({ id: NEW_CUSTOMER_ID, name: body.name, phone: body.phone ?? "", customer_group_name: null });
      return HttpResponse.json(
        {
          id: NEW_CUSTOMER_ID,
          kind: "person",
          name: body.name,
          phone: body.phone,
          email: null,
          tax_code: null,
          address: null,
          note: null,
          version: 1,
          customer_group_id: "grp-le",
          customer_group_name: "Khách lẻ",
          credit_limit: null,
          payment_terms_days: body.payment_terms_days,
          status: "active",
          is_supplier: false,
          duplicate_warnings: [],
        },
        { status: 201 },
      );
    }),
  );

  return {
    calls,
    /** The writes sent to one path, in order. */
    to: (path: string) => calls.filter((c) => c.path === path),
  };
}
