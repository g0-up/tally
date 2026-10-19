// API payloads of the Khách hàng screen, shaped as OpenAPI returns them (the test tenant's business
// date is 2026-10-12). Money is a plain decimal string.

export const CUSTOMER_ID = "cus-an";
export const RETAIL_GROUP_ID = "grp-le";
export const WHOLESALE_GROUP_ID = "grp-si";

export function testCustomer(over: Record<string, unknown> = {}) {
  return {
    id: CUSTOMER_ID,
    kind: "organization",
    code: "KH-0001",
    name: "Tạp hóa Minh An",
    phone: "0903123456",
    email: null,
    tax_code: null,
    address: "12 Lê Lợi, Q.1",
    note: null,
    version: 4,
    customer_group_id: WHOLESALE_GROUP_ID,
    customer_group_name: "Khách sỉ",
    credit_limit: "50000000",
    payment_terms_days: 15,
    status: "active",
    is_supplier: false,
    ...over,
  };
}

/** A row of `GET /customers`: the customer with its latest purchase and current consents. */
export function testListedCustomer(over: Record<string, unknown> = {}) {
  return {
    ...testCustomer(),
    created_at: "2026-09-01T03:00:00Z",
    updated_at: "2026-10-02T03:00:00Z",
    last_purchase_at: null,
    consents: [],
    ...over,
  };
}

/** A current consent as the customer list carries it. */
export function testListedConsent(over: Record<string, unknown> = {}) {
  return { channel: "zalo", purpose: "marketing", granted: true, recorded_at: "2026-10-02T03:00:00Z", ...over };
}

export const GROUPS = [
  { id: RETAIL_GROUP_ID, name: "Khách lẻ", base_price_kind: "retail", is_system: true, active: true },
  { id: WHOLESALE_GROUP_ID, name: "Khách sỉ", base_price_kind: "wholesale", is_system: false, active: true },
];

export function testPriceList(over: Record<string, unknown> = {}) {
  return {
    id: "pl-si",
    name: "Bảng giá sỉ mùa Tết",
    kind: "group",
    customer_group_id: WHOLESALE_GROUP_ID,
    party_id: null,
    priority: 20,
    active: true,
    ...over,
  };
}

export function testConsent(over: Record<string, unknown> = {}) {
  return {
    id: "con-1",
    party_id: CUSTOMER_ID,
    channel: "zalo",
    purpose: "marketing",
    granted: true,
    source: "tại quầy",
    evidence: null,
    recorded_by: "usr-1",
    recorded_at: "2026-10-02T03:00:00Z",
    ...over,
  };
}

export function testReorderDraft(over: Record<string, unknown> = {}) {
  return {
    invoice_id: "inv-77",
    invoice_doc_no: "SO-0077",
    invoice_version: 1,
    total_amount: "4200000",
    source_document_id: "doc-42",
    source_doc_no: "SO-0042",
    lines: [
      { sku_id: "sku-bq454", name: "Bánh quy bơ hộp 454g", qty: "10", copied: true, flag: null },
      { sku_id: "sku-kd350", name: "Kẹo dừa hộp 350g", qty: "5", copied: false, flag: "discontinued" },
    ],
    ...over,
  };
}
