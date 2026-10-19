import { describe, expect, it } from "vitest";
import { ApiError, apiErrorFromBody, errorMessage, unknownErrorMessage } from "../../../shared/api/api-error";
import { DEBT_ERROR_MESSAGES, withDebtMessage } from "./adapters";

describe("debt refusals", () => {
  it.each(Object.entries(DEBT_ERROR_MESSAGES))("reads %s in Vietnamese, never the server's English", (code, text) => {
    const e = withDebtMessage(apiErrorFromBody(409, { error: { code, message: "English text" } }));
    expect(e).toBeInstanceOf(ApiError);
    expect(errorMessage(e)).toBe(text);
    expect((e as ApiError).code).toBe(code);
  });

  it.each([
    ["not_found", 404, "Không tìm thấy dữ liệu, có thể đã bị xóa. Tải lại để xem bản mới nhất."],
    ["validation_failed", 422, "Kiểm tra lại các ô được đánh dấu."],
    ["invalid_cursor", 422, "Danh sách đã thay đổi. Tải lại để xem từ đầu."],
  ])("leaves the shared code %s to the shared sentence", (code, status, text) => {
    const raw = apiErrorFromBody(status, { error: { code, message: "English text" } });
    expect(withDebtMessage(raw)).toBe(raw);
    expect(errorMessage(raw)).toBe(text);
  });

  it("falls back to a Vietnamese sentence for a code nobody knows", () => {
    const e = withDebtMessage(apiErrorFromBody(409, { error: { code: "ledger_frozen_somewhere", message: "Ledger is frozen" } }));
    expect(errorMessage(e)).toBe(unknownErrorMessage("ledger_frozen_somewhere"));
    expect(errorMessage(e)).not.toContain("Ledger is frozen");
  });

  it("keeps what is not a server refusal", () => {
    const network = new TypeError("Failed to fetch");
    expect(withDebtMessage(network)).toBe(network);
  });
});
