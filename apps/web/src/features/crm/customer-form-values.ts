import { z } from "zod";
import { fieldReasonMessage, isApiError } from "../../shared/api/api-error";
import { dec } from "../../shared/lib/decimal";
import type { Money } from "../../shared/domain/money";
import type { Consent, ConsentChannel, Customer, CustomerGroup, PriceList } from "./schemas";

// The customer form ("Thêm khách hàng" / "Sửa khách hàng"): the zod checks the client repeats,
// the request bodies, and the server answers mapped onto the same sentences. "Đồng ý nhận tin" is
// the marketing consent, recorded through the append-only consents endpoint, never on the customer.

export const PHONE_PATTERN = /^[0-9 .+()-]{8,20}$/;
export const TAX_CODE_PATTERN = /^[0-9]{10}(-[0-9]{3})?$/;
export const MAX_TERMS_DAYS = 365;

export const CUSTOMER_MESSAGES = {
  nameRequired: "Nhập tên khách hàng.",
  phoneInvalid: "Số điện thoại không hợp lệ.",
  emailInvalid: "Email không hợp lệ.",
  taxCodeInvalid: "Mã số thuế gồm 10 chữ số, có thể thêm “-” và 3 chữ số.",
  termsRange: "Số ngày nợ từ 0 đến 365.",
  creditNegative: "Hạn mức nợ không được âm.",
  consentSourceRequired: "Ghi nơi khách đồng ý hoặc rút đồng ý (tại quầy, qua điện thoại…).",
} as const;

/**
 * Server refusals of customer and CRM commands, in the screen's words; `not_found` and
 * `invalid_cursor` read the shared sentences.
 */
export const CRM_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  already_customer: "Đối tác này đã là khách hàng.",
  no_purchase: "Khách chưa có lần mua nào để tái đặt.",
  nothing_to_reorder: "Không dòng nào của lần mua trước còn bán được. Dùng “Tạo đơn”.",
  reorder_unavailable: "Chưa tái đặt đơn được lúc này. Dùng “Tạo đơn”.",
  no_closed_campaign: "Chưa có chiến dịch nào đã kết thúc để so sánh.",
  loyalty_disabled: "Chương trình tích điểm đang tắt.",
  insufficient_points: "Khách không đủ điểm.",
  already_redeemed: "Chứng từ này đã được đổi điểm.",
};

/** Refusals of the customers the revenue column names (`customer_ids`), which the screen builds itself. */
const REVENUE_CUSTOMERS_MESSAGES: Readonly<Record<string, string>> = {
  too_many: "Mỗi lần chỉ đọc được doanh thu của tối đa 100 khách nên cột doanh thu hiện “—”.",
  invalid: "Danh sách khách để đọc doanh thu không hợp lệ nên cột doanh thu hiện “—”. Tải lại trang rồi thử lại.",
};

/** Why the revenue column reads "—" when the report refused its customers; null for any other refusal, which stays silent. */
export function revenueRefusalMessage(e: unknown): string | null {
  if (!isApiError(e)) return null;
  const reason = e.fields.customer_ids;
  return reason === undefined ? null : (REVENUE_CUSTOMERS_MESSAGES[reason] ?? null);
}

export const CONSENT_CHANNEL_LABELS: Readonly<Record<ConsentChannel, string>> = {
  zalo: "Zalo",
  sms: "SMS",
  phone: "Điện thoại",
  email: "Email",
};

export interface CustomerDraft {
  kind: "person" | "organization";
  name: string;
  phone: string;
  email: string;
  taxCode: string;
  address: string;
  note: string;
  /** Empty lets the server pick the retail group. */
  groupId: string;
  /** Null means no limit. */
  creditLimit: Money | null;
  /** Payment terms in days, as typed. */
  terms: string;
  status: "active" | "inactive";
  /** "Đồng ý nhận tin": marketing messages on `consentChannel`. */
  consent: boolean;
  consentChannel: ConsentChannel;
  /** Where the consent was given or withdrawn; required whenever it changes. */
  consentSource: string;
}

export type CustomerField = "name" | "phone" | "email" | "taxCode" | "address" | "note" | "groupId" | "creditLimit" | "terms" | "consentSource";
export type CustomerErrors = Partial<Record<CustomerField, string>>;

export const EMPTY_CUSTOMER: CustomerDraft = {
  kind: "person",
  name: "",
  phone: "",
  email: "",
  taxCode: "",
  address: "",
  note: "",
  groupId: "",
  creditLimit: null,
  terms: "0",
  status: "active",
  consent: false,
  consentChannel: "zalo",
  consentSource: "",
};

/** Channels on which the customer currently accepts marketing messages. */
export function marketingChannels(consents: readonly Consent[] | undefined): ConsentChannel[] {
  return (consents ?? []).filter((c) => c.purpose === "marketing" && c.granted).map((c) => c.channel);
}

export function customerDraftOf(c: Customer, granted: readonly ConsentChannel[]): CustomerDraft {
  return {
    kind: c.kind,
    name: c.name,
    phone: c.phone ?? "",
    email: c.email ?? "",
    taxCode: c.tax_code ?? "",
    address: c.address ?? "",
    note: c.note ?? "",
    groupId: c.customer_group_id,
    creditLimit: c.credit_limit,
    terms: String(c.payment_terms_days),
    status: c.status,
    consent: granted.length > 0,
    consentChannel: granted[0] ?? "zalo",
    consentSource: "",
  };
}

/** Consent records the draft adds: grant on the chosen channel, or withdraw every granted one. */
export function consentChanges(d: Pick<CustomerDraft, "consent" | "consentChannel">, granted: readonly ConsentChannel[]): { channel: ConsentChannel; granted: boolean }[] {
  if (d.consent) return granted.includes(d.consentChannel) ? [] : [{ channel: d.consentChannel, granted: true }];
  return granted.map((channel) => ({ channel, granted: false }));
}

const optional = (pattern: RegExp, message: string) =>
  z
    .string()
    .trim()
    .refine((v) => v === "" || pattern.test(v), message);

/** Client checks in form order. */
export const customerFormSchema = z.object({
  name: z.string().trim().min(1, CUSTOMER_MESSAGES.nameRequired).max(200),
  phone: optional(PHONE_PATTERN, CUSTOMER_MESSAGES.phoneInvalid),
  email: z
    .string()
    .trim()
    .refine((v) => v === "" || z.email().safeParse(v).success, CUSTOMER_MESSAGES.emailInvalid),
  taxCode: optional(TAX_CODE_PATTERN, CUSTOMER_MESSAGES.taxCodeInvalid),
  creditLimit: z
    .string()
    .nullable()
    .refine((v) => v === null || !dec(v).isNegative(), CUSTOMER_MESSAGES.creditNegative),
  terms: z
    .string()
    .trim()
    .refine((v) => /^\d+$/.test(v === "" ? "0" : v) && Number(v === "" ? "0" : v) <= MAX_TERMS_DAYS, CUSTOMER_MESSAGES.termsRange),
});

const CHECKED: readonly CustomerField[] = ["name", "phone", "email", "taxCode", "creditLimit", "terms"];

/** Field errors of the client checks; empty when the draft may be sent. */
export function validateCustomerDraft(d: CustomerDraft, granted: readonly ConsentChannel[]): CustomerErrors {
  const out: CustomerErrors = {};
  const r = customerFormSchema.safeParse(d);
  if (!r.success) {
    for (const issue of r.error.issues) {
      const field = CHECKED.find((f) => f === issue.path[0]);
      if (field !== undefined && out[field] === undefined) out[field] = issue.message;
    }
  }
  if (consentChanges(d, granted).length > 0 && d.consentSource.trim() === "") out.consentSource = CUSTOMER_MESSAGES.consentSourceRequired;
  return out;
}

/** The first error in form order, for the "Chưa ghi sổ." toast. */
export function firstCustomerError(errors: CustomerErrors): string | undefined {
  const order: readonly CustomerField[] = [...CHECKED, "consentSource"];
  for (const f of order) if (errors[f] !== undefined) return errors[f];
  return undefined;
}

const text = (v: string): string | null => (v.trim() === "" ? null : v.trim());

/** `POST /customers` and `PATCH /customers/{id}` body: every field of the form, null clears it. */
export function customerRequest(d: CustomerDraft) {
  return {
    kind: d.kind,
    name: d.name.trim(),
    phone: text(d.phone),
    email: text(d.email),
    tax_code: text(d.taxCode),
    address: text(d.address),
    note: text(d.note),
    ...(d.groupId !== "" ? { customer_group_id: d.groupId } : {}),
    credit_limit: d.creditLimit,
    payment_terms_days: Number(d.terms.trim() === "" ? "0" : d.terms.trim()),
    status: d.status,
  };
}

/** `POST /customers/{id}/consents` body of one consent change. */
export function consentRequest(change: { channel: ConsentChannel; granted: boolean }, source: string) {
  return { channel: change.channel, purpose: "marketing" as const, granted: change.granted, source: source.trim() };
}

const API_FIELD: Readonly<Record<string, CustomerField>> = {
  name: "name",
  phone: "phone",
  email: "email",
  tax_code: "taxCode",
  address: "address",
  note: "note",
  customer_group_id: "groupId",
  credit_limit: "creditLimit",
  payment_terms_days: "terms",
  source: "consentSource",
};

/** Form errors of a refused customer command, in the form's words where it has them. */
export function customerFormErrors(e: unknown): CustomerErrors {
  if (!isApiError(e)) return {};
  const out: CustomerErrors = {};
  for (const [path, reason] of Object.entries(e.fields)) {
    const field = API_FIELD[path];
    if (field === undefined || out[field] !== undefined) continue;
    if (field === "taxCode") out.taxCode = CUSTOMER_MESSAGES.taxCodeInvalid;
    else if (field === "email") out.email = CUSTOMER_MESSAGES.emailInvalid;
    else if (field === "terms") out.terms = CUSTOMER_MESSAGES.termsRange;
    else out[field] = fieldReasonMessage(reason);
  }
  return out;
}

/** The same refusal with the screen's sentence as its message, for the "Chưa ghi sổ." toast. */
export function withCrmMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = CRM_ERROR_MESSAGES[e.code];
  return message === undefined ? e : e.withMessage(message);
}

const BASE_PRICE_LABELS = { retail: "Giá bán lẻ", wholesale: "Giá bán sỉ" } as const;

/**
 * The price lists the customer gets, highest priority first (prototype Bảng giá: giá riêng khách →
 * bảng giá nhóm khách → giá gốc). The API has no price-list field on a customer: the group decides.
 */
export function priceSourceText(group: Pick<CustomerGroup, "id" | "base_price_kind"> | undefined, lists: readonly PriceList[], customerId: string | null): string {
  const byPriority = [...lists].sort((a, b) => a.priority - b.priority);
  const own = customerId === null ? [] : byPriority.filter((l) => l.kind === "customer" && l.party_id === customerId);
  const ofGroup = group === undefined ? [] : byPriority.filter((l) => l.kind === "group" && l.customer_group_id === group.id);
  const names = [...own, ...ofGroup].map((l) => l.name);
  if (group !== undefined) names.push(BASE_PRICE_LABELS[group.base_price_kind]);
  return names.length > 0 ? names.join(" → ") : "—";
}
