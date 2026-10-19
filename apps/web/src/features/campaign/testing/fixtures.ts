// API payloads of the seasons screen, shaped as OpenAPI returns them (the test tenant's business
// date is 2026-10-12). Money and quantities are plain decimal strings.

export const TET_ID = "camp-tet";
export const SUMMER_ID = "camp-he";

/** The item at `i`, failing the test when the list is shorter. */
export function at<T>(items: readonly T[], i: number): T {
  const item = items[i];
  if (item === undefined) throw new Error(`No item at ${i} of ${items.length}`);
  return item;
}

export function testCampaign(over: Record<string, unknown> = {}) {
  return {
    id: TET_ID,
    name: "Tết Đinh Mùi 2027",
    kind: "tet",
    starts_on: "2026-10-01",
    ends_on: "2027-02-28",
    budget_amount: "25000000",
    target_revenue: "800000000",
    target_margin_percent: "20",
    status: "active",
    closed_at: null,
    version: 3,
    ...over,
  };
}

export function testChecklistItem(over: Record<string, unknown> = {}) {
  return {
    id: "chk-1",
    phase: "before_peak",
    kind: "custom",
    title: "Chốt bảng giá sỉ mùa Tết",
    done: true,
    done_at: "2026-10-05T03:00:00Z",
    done_by: "usr-1",
    sort_order: 1,
    ...over,
  };
}

export const CHECKLIST = [
  testChecklistItem(),
  testChecklistItem({ id: "chk-2", title: "Đặt đủ hộp quà với nhà cung cấp", done: false, done_at: null, done_by: null, sort_order: 2 }),
  testChecklistItem({ id: "chk-3", phase: "closing", title: "Đối chiếu công nợ khách sỉ", done: false, done_at: null, done_by: null, sort_order: 3 }),
];

/** `GET /reports/season` summary as the API orders it. */
export function testSeasonReport(over: Record<string, string | null> = {}) {
  const values: Record<string, string | null> = {
    net_revenue: "120000000",
    target_revenue: "800000000",
    revenue_achieved: "15",
    returns_amount: "2000000",
    cogs: "102000000",
    gross_margin: "18000000",
    margin_percent: "15",
    target_margin_percent: "20",
    budget: "25000000",
    open_orders: "30000000",
    receivable: "40000000",
    documents: "42",
    customers: "17",
    days: "151",
    ...over,
  };
  return {
    report_key: "season",
    title: "Kết quả mùa",
    summary: Object.entries(values).map(([key, value]) => ({ key, label: key, kind: "money", value, previous: null })),
    rows: [],
    next_cursor: null,
  };
}

export function testDemandRow(over: Record<string, unknown> = {}) {
  return {
    sku_id: "sku-bq454",
    code: "BQ454",
    name: "Bánh quy bơ hộp 454g",
    available: "120",
    backlog: "40",
    undated_incoming: "0",
    first_shortage_on: "2026-10-20",
    max_shortage: "60",
    days: [
      { date: "2026-10-01", demand: "100", incoming: "0", projected: "20", shortage: "0" },
      { date: "2026-10-15", demand: "80", incoming: "50", projected: "-10", shortage: "10" },
      { date: "2026-10-20", demand: "0", incoming: "0", projected: "-60", shortage: "60" },
    ],
    ...over,
  };
}
