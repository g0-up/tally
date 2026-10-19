import { z } from "zod";
import { ApiError, fieldReasonMessage, isApiError } from "../../shared/api/api-error";
import { normalizeText } from "../../shared/lib/textnorm";
import type { Supplier, SupplierListItem } from "./schemas";

// The supplier form (prototype supModal / supQuick) in the prototype's words: the zod checks
// the client repeats, the request bodies, and the server answers mapped onto the same sentences.

export const PHONE_PATTERN = /^[0-9 .+()-]{8,20}$/;
export const MAX_TERMS_DAYS = 365;

export const SUPPLIER_MESSAGES = {
  nameRequired: "Nhập tên nhà cung cấp.",
  nameTaken: (name: string) => `Đã có nhà cung cấp “${name}”.`,
  phoneInvalid: "Số điện thoại không hợp lệ.",
  phoneTaken: "Số điện thoại này đã gắn với nhà cung cấp khác.",
  termsRange: "Số ngày nợ từ 0 đến 365.",
} as const;

/** Server refusals of supplier commands, in the prototype's words. */
export const SUPPLIER_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  supplier_has_open_orders: "Còn đơn nhập đang mở với nhà cung cấp này. Nhận hết hoặc đóng đơn trước khi ngừng giao dịch.",
  supplier_in_use: "Nhà cung cấp đã có chứng từ (đơn nhập, phiếu nhận hoặc thanh toán) nên không xóa được. Dùng “Ngừng giao dịch” để ẩn khỏi danh sách chọn.",
  party_in_use: "Đối tác này đã có giao dịch bán hàng nên không xóa được.",
};

export interface SupplierDraft {
  name: string;
  contact: string;
  phone: string;
  taxCode: string;
  address: string;
  /** Payment terms in days, as typed. */
  terms: string;
  goods: string;
  note: string;
  /** The partner also buys from the shop (a customer profile on the same party). */
  alsoCustomer: boolean;
}

export type SupplierField = "name" | "phone" | "terms" | "taxCode" | "address" | "goods" | "note" | "contact";
export type SupplierErrors = Partial<Record<SupplierField, string>>;

export const EMPTY_SUPPLIER: SupplierDraft = { name: "", contact: "", phone: "", taxCode: "", address: "", terms: "30", goods: "", note: "", alsoCustomer: false };

export function supplierDraftOf(s: Supplier, contactName: string): SupplierDraft {
  return {
    name: s.name,
    contact: contactName,
    phone: s.phone ?? "",
    taxCode: s.tax_code ?? "",
    address: s.address ?? "",
    terms: String(s.payment_terms_days),
    goods: s.supplied_goods_note ?? "",
    note: s.note ?? "",
    alsoCustomer: s.is_customer,
  };
}

/** Client checks in prototype order (saveSupplier): name, phone, terms. */
export const supplierFormSchema = z.object({
  name: z.string().trim().min(1, SUPPLIER_MESSAGES.nameRequired),
  phone: z
    .string()
    .trim()
    .refine((v) => v === "" || PHONE_PATTERN.test(v), SUPPLIER_MESSAGES.phoneInvalid),
  terms: z
    .string()
    .trim()
    .refine((v) => {
      const n = Math.round(Number(v === "" ? "0" : v));
      return Number.isFinite(n) && n >= 0 && n <= MAX_TERMS_DAYS;
    }, SUPPLIER_MESSAGES.termsRange),
});

/** Field errors of the client checks; empty when the draft may be sent. */
export function validateSupplierDraft(d: Pick<SupplierDraft, "name" | "phone" | "terms">): SupplierErrors {
  const r = supplierFormSchema.safeParse(d);
  if (r.success) return {};
  const out: SupplierErrors = {};
  for (const issue of r.error.issues) {
    const field = issue.path[0];
    if ((field === "name" || field === "phone" || field === "terms") && out[field] === undefined) out[field] = issue.message;
  }
  return out;
}

/** Another supplier already uses this name or phone (prototype blocks both); the server only warns. */
export function duplicateSupplierErrors(d: Pick<SupplierDraft, "name" | "phone">, others: readonly Pick<SupplierListItem, "id" | "name" | "phone">[], selfId: string | null): SupplierErrors {
  const name = normalizeText(d.name.trim());
  const digits = d.phone.replace(/\D/g, "");
  const rest = others.filter((s) => s.id !== selfId);
  if (rest.some((s) => normalizeText(s.name) === name)) return { name: SUPPLIER_MESSAGES.nameTaken(d.name.trim()) };
  if (digits !== "" && rest.some((s) => s.phone !== null && s.phone.replace(/\D/g, "") === digits)) return { phone: SUPPLIER_MESSAGES.phoneTaken };
  return {};
}

const text = (v: string): string | null => (v.trim() === "" ? null : v.trim());
const termsOf = (v: string): number => Math.round(Number(v.trim() === "" ? "0" : v.trim()));

/** `POST /suppliers` body of the full form or of the quick form (name, phone, terms only). */
export function supplierCreateRequest(d: SupplierDraft, quick = false) {
  if (quick) return { kind: "organization" as const, name: d.name.trim(), phone: text(d.phone), payment_terms_days: termsOf(d.terms) };
  return { kind: "organization" as const, ...supplierUpdateRequest(d) };
}

/** `PATCH /suppliers/{id}` body: every field of the form, null clears it. */
export function supplierUpdateRequest(d: SupplierDraft) {
  return {
    name: d.name.trim(),
    phone: text(d.phone),
    tax_code: text(d.taxCode),
    address: text(d.address),
    note: text(d.note),
    payment_terms_days: termsOf(d.terms),
    supplied_goods_note: text(d.goods),
  };
}

const API_FIELD: Readonly<Record<string, SupplierField>> = {
  name: "name",
  phone: "phone",
  payment_terms_days: "terms",
  tax_code: "taxCode",
  address: "address",
  supplied_goods_note: "goods",
  note: "note",
};

/** Form errors of a refused supplier command, in the prototype's words where it has them. */
export function supplierFormErrors(e: unknown): SupplierErrors {
  if (!isApiError(e)) return {};
  const out: SupplierErrors = {};
  for (const [path, reason] of Object.entries(e.fields)) {
    const field = API_FIELD[path] ?? (path.startsWith("contact") ? "contact" : undefined);
    if (field === undefined || out[field] !== undefined) continue;
    if (field === "phone" && reason === "invalid") out.phone = SUPPLIER_MESSAGES.phoneInvalid;
    else if (field === "terms") out.terms = SUPPLIER_MESSAGES.termsRange;
    else out[field] = fieldReasonMessage(reason);
  }
  return out;
}

/** The same refusal with the prototype sentence as its message, for the "Chưa ghi sổ." toast. */
export function withSupplierMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = SUPPLIER_ERROR_MESSAGES[e.code];
  if (message === undefined) return e;
  return new ApiError({ status: e.status, code: e.code, message, fields: { ...e.fields }, exceptionKind: e.exceptionKind });
}
