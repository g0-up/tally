import { describe, expect, it } from "vitest";
import { ApiError, apiErrorFromBody, errorMessage, unknownErrorMessage } from "../../shared/api/api-error";
import type { Money } from "../../shared/domain/money";
import { seasonStart } from "./api/queries";
import {
  CUSTOMER_MESSAGES,
  EMPTY_CUSTOMER,
  consentChanges,
  customerDraftOf,
  customerFormErrors,
  customerRequest,
  firstCustomerError,
  marketingChannels,
  priceSourceText,
  validateCustomerDraft,
  withCrmMessage,
} from "./customer-form-values";
import { consentsSchema, customerSchema, priceListSchema } from "./schemas";
import { CUSTOMER_ID, RETAIL_GROUP_ID, WHOLESALE_GROUP_ID, testConsent, testCustomer, testPriceList } from "./testing/fixtures";

describe("seasonStart", () => {
  it("is 1 October of the selling season the business date falls in", () => {
    expect(seasonStart("2026-10-12")).toBe("2026-10-01");
    expect(seasonStart("2026-10-01")).toBe("2026-10-01");
    expect(seasonStart("2027-02-03")).toBe("2026-10-01");
    expect(seasonStart("2026-09-30")).toBe("2025-10-01");
  });
});

describe("marketingChannels", () => {
  it("lists the channels with a granted marketing consent only", () => {
    const consents = consentsSchema.parse({
      current: [testConsent(), testConsent({ channel: "sms", granted: false }), testConsent({ channel: "phone", purpose: "debt_reminder" })],
      history: [],
    });
    expect(marketingChannels(consents.current)).toEqual(["zalo"]);
    expect(marketingChannels(undefined)).toEqual([]);
  });
});

describe("consentChanges", () => {
  it("grants the chosen channel once and withdraws every granted channel", () => {
    expect(consentChanges({ consent: true, consentChannel: "zalo" }, [])).toEqual([{ channel: "zalo", granted: true }]);
    expect(consentChanges({ consent: true, consentChannel: "zalo" }, ["zalo"])).toEqual([]);
    expect(consentChanges({ consent: false, consentChannel: "zalo" }, ["zalo", "sms"])).toEqual([
      { channel: "zalo", granted: false },
      { channel: "sms", granted: false },
    ]);
    expect(consentChanges({ consent: false, consentChannel: "zalo" }, [])).toEqual([]);
  });
});

describe("validateCustomerDraft", () => {
  it("accepts a name alone", () => {
    expect(validateCustomerDraft({ ...EMPTY_CUSTOMER, name: "Cô Ba" }, [])).toEqual({});
  });

  it("checks each field with the screen's sentences, first error in form order", () => {
    const errors = validateCustomerDraft(
      { ...EMPTY_CUSTOMER, name: "  ", phone: "abc", email: "a@", taxCode: "12345", creditLimit: "-1" as Money, terms: "400", consent: true, consentSource: " " },
      [],
    );
    expect(errors).toEqual({
      name: CUSTOMER_MESSAGES.nameRequired,
      phone: CUSTOMER_MESSAGES.phoneInvalid,
      email: CUSTOMER_MESSAGES.emailInvalid,
      taxCode: CUSTOMER_MESSAGES.taxCodeInvalid,
      creditLimit: CUSTOMER_MESSAGES.creditNegative,
      terms: CUSTOMER_MESSAGES.termsRange,
      consentSource: CUSTOMER_MESSAGES.consentSourceRequired,
    });
    expect(firstCustomerError(errors)).toBe("Nhập tên khách hàng.");
  });

  it("asks for the consent source only when the consent changes", () => {
    expect(validateCustomerDraft({ ...EMPTY_CUSTOMER, name: "Cô Ba", consent: true }, ["zalo"])).toEqual({});
    expect(validateCustomerDraft({ ...EMPTY_CUSTOMER, name: "Cô Ba", consent: false }, ["zalo"])).toEqual({ consentSource: CUSTOMER_MESSAGES.consentSourceRequired });
  });

  it("takes a tax code with a branch suffix", () => {
    expect(validateCustomerDraft({ ...EMPTY_CUSTOMER, name: "Cô Ba", taxCode: "0312345678-001" }, [])).toEqual({});
  });
});

describe("customerRequest", () => {
  it("sends every form field, null for an empty one, and leaves the group to the server when unset", () => {
    expect(customerRequest({ ...EMPTY_CUSTOMER, name: " Cô Ba ", phone: " 0903 111 222 ", terms: "" })).toEqual({
      kind: "person",
      name: "Cô Ba",
      phone: "0903 111 222",
      email: null,
      tax_code: null,
      address: null,
      note: null,
      credit_limit: null,
      payment_terms_days: 0,
      status: "active",
    });
  });

  it("round-trips a customer through the draft", () => {
    const customer = customerSchema.parse(testCustomer());
    const draft = customerDraftOf(customer, ["sms"]);
    expect(draft).toMatchObject({ consent: true, consentChannel: "sms", consentSource: "" });
    expect(customerRequest(draft)).toEqual({
      kind: "organization",
      name: "Tạp hóa Minh An",
      phone: "0903123456",
      email: null,
      tax_code: null,
      address: "12 Lê Lợi, Q.1",
      note: null,
      customer_group_id: WHOLESALE_GROUP_ID,
      credit_limit: "50000000",
      payment_terms_days: 15,
      status: "active",
    });
  });
});

describe("customerFormErrors and withCrmMessage", () => {
  it("maps API field paths onto the form's fields and sentences", () => {
    const e = new ApiError({ status: 422, code: "validation_failed", message: "Invalid", fields: { tax_code: "pattern", payment_terms_days: "minimum", name: "required", unknown: "x" } });
    const errors = customerFormErrors(e);
    expect(errors.taxCode).toBe(CUSTOMER_MESSAGES.taxCodeInvalid);
    expect(errors.terms).toBe(CUSTOMER_MESSAGES.termsRange);
    expect(errors.name).toBeTruthy();
    expect(Object.keys(errors).sort()).toEqual(["name", "taxCode", "terms"]);
    expect(customerFormErrors(new Error("x"))).toEqual({});
  });

  it("puts the screen's sentence on a known refusal", () => {
    const e = withCrmMessage(new ApiError({ status: 409, code: "nothing_to_reorder", message: "Nothing", fields: {} }));
    expect((e as ApiError).message).toBe("Không dòng nào của lần mua trước còn bán được. Dùng “Tạo đơn”.");
  });

  // Every code the server's customer and CRM modules answer with.
  it.each([
    ["already_customer", 409, "Đối tác này đã là khách hàng."],
    ["no_purchase", 409, "Khách chưa có lần mua nào để tái đặt."],
    ["nothing_to_reorder", 409, "Không dòng nào của lần mua trước còn bán được. Dùng “Tạo đơn”."],
    ["reorder_unavailable", 409, "Chưa tái đặt đơn được lúc này. Dùng “Tạo đơn”."],
    ["no_closed_campaign", 422, "Chưa có chiến dịch nào đã kết thúc để so sánh."],
    ["loyalty_disabled", 409, "Chương trình tích điểm đang tắt."],
    ["insufficient_points", 409, "Khách không đủ điểm."],
    ["already_redeemed", 409, "Chứng từ này đã được đổi điểm."],
    ["not_found", 404, "Không tìm thấy dữ liệu, có thể đã bị xóa. Tải lại để xem bản mới nhất."],
    ["invalid_cursor", 422, "Danh sách đã thay đổi. Tải lại để xem từ đầu."],
    ["validation_failed", 422, "Kiểm tra lại các ô được đánh dấu."],
  ])("reads %s in Vietnamese", (code, status, text) => {
    const e = withCrmMessage(apiErrorFromBody(status, { error: { code, message: "English text" } }));
    expect(errorMessage(e)).toBe(text);
    expect(errorMessage(e)).not.toBe(unknownErrorMessage(code));
  });
});

describe("priceSourceText", () => {
  const lists = [
    testPriceList({ id: "pl-b", name: "Sỉ phụ", priority: 50 }),
    testPriceList(),
    testPriceList({ id: "pl-own", name: "Giá riêng Minh An", kind: "customer", customer_group_id: null, party_id: CUSTOMER_ID, priority: 90 }),
    testPriceList({ id: "pl-other", name: "Giá riêng khách khác", kind: "customer", customer_group_id: null, party_id: "cus-x", priority: 1 }),
  ].map((l) => priceListSchema.parse(l));

  it("names the customer's own lists, then the group's by priority, then the base price", () => {
    expect(priceSourceText({ id: WHOLESALE_GROUP_ID, base_price_kind: "wholesale" }, lists, CUSTOMER_ID)).toBe("Giá riêng Minh An → Bảng giá sỉ mùa Tết → Sỉ phụ → Giá bán sỉ");
    expect(priceSourceText({ id: RETAIL_GROUP_ID, base_price_kind: "retail" }, lists, null)).toBe("Giá bán lẻ");
    expect(priceSourceText(undefined, [], null)).toBe("—");
  });
});
