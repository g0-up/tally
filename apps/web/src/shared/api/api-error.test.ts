import { describe, expect, it } from "vitest";
import { ApiError, apiErrorFromBody, errorMessage, fieldReasonMessage, networkError, unknownErrorMessage } from "./api-error";

const fromServer = (status: number, code: string, message: string) => apiErrorFromBody(status, { error: { code, message } });

describe("errorMessage", () => {
  it("never shows the server's English text for a code no screen translates", () => {
    const e = fromServer(409, "payment_has_successor", "The payment has a successor");
    expect(e.fromServer).toBe(true);
    expect(errorMessage(e)).toBe("Máy chủ từ chối thao tác (mã payment_has_successor).");
    expect(errorMessage(e)).toBe(unknownErrorMessage("payment_has_successor"));
  });

  it.each([
    ["not_found", 404, "Không tìm thấy dữ liệu, có thể đã bị xóa. Tải lại để xem bản mới nhất."],
    ["invalid_cursor", 422, "Danh sách đã thay đổi. Tải lại để xem từ đầu."],
    ["idempotency_key_in_progress", 409, "Thao tác này đang được xử lý. Chờ một chút rồi tải lại để xem kết quả."],
    ["idempotency_key_reused", 409, "Thao tác này đã được ghi với nội dung khác. Tải lại để kiểm tra chứng từ đã ghi trước khi làm lại."],
    ["idempotency_key_invalid", 422, "Yêu cầu không hợp lệ, hãy tải lại trang rồi thử lại."],
    ["exception_confirmation_used", 409, "Xác nhận này đã được dùng. Xác nhận lại rồi thử lại."],
  ])("translates the shared code %s", (code, status, text) => {
    expect(errorMessage(fromServer(status, code, "English text"))).toBe(text);
  });

  it.each([
    ["tenant_closed", 403, "Cửa hàng đã đóng. Chỉ còn tải được bản xuất dữ liệu cuối cùng."],
    ["closure_state_invalid", 409, "Yêu cầu đóng tài khoản đã chuyển sang bước khác. Tải lại để xem trạng thái mới."],
    ["support_session_inactive", 403, "Phiên hỗ trợ đã hết hạn hoặc đã bị thu hồi."],
    ["possible_password", 422, "Nội dung có thể chứa mật khẩu. Không gửi mật khẩu cho nhân viên hỗ trợ."],
    ["period_locked", 423, "Kỳ kế toán của ngày này đã khóa."],
    ["reopen_not_backward", 422, "Ngày mở lại phải trước ngày đang khóa."],
    ["export_queue_full", 409, "Đang có bản xuất chưa xong. Chờ bản đó hoàn tất rồi thử lại."],
    ["step_not_skippable", 409, "Bước này bắt buộc, không bỏ qua được."],
    ["opening_balances_confirmed", 409, "Số dư đầu kỳ đã xác nhận, không sửa được nữa."],
    ["invalid_code", 422, "Mã xác thực không đúng hoặc đã hết hạn."],
    ["sandbox_not_billable", 409, "Cửa hàng mẫu không có thuê bao, hóa đơn hay yêu cầu đóng tài khoản."],
  ])("translates the store and platform code %s", (code, status, text) => {
    expect(errorMessage(fromServer(status, code, "English text"))).toBe(text);
  });

  it("names the wait of a throttled request whatever its code", () => {
    const e = apiErrorFromBody(429, { error: { code: "rate_limited", message: "Too many requests" } }, 90);
    expect(errorMessage(e)).toBe("Thao tác quá nhanh. Thử lại sau 2 phút.");
  });

  it("keeps the Vietnamese sentence a screen put on a refusal, with its wait", () => {
    const translated = fromServer(429, "rate_limited", "Slow down").withMessage("Câu của màn hình.");
    expect(translated.fromServer).toBe(false);
    expect(translated.code).toBe("rate_limited");
    expect(translated.status).toBe(429);
    const e = apiErrorFromBody(409, { error: { code: "payment_reversed", message: "The payment has been reversed" } }, 30).withMessage("Phiếu đã bị hủy.");
    expect(e.retryAfterSeconds).toBe(30);
    expect(errorMessage(e)).toBe("Phiếu đã bị hủy.");
  });

  it("shows the client's own errors as they are", () => {
    expect(errorMessage(networkError())).toBe("Không kết nối được máy chủ. Kiểm tra mạng rồi thử lại.");
    expect(errorMessage(new ApiError({ status: 422, code: "upload_failed", message: "Không tải được tệp." }))).toBe("Không tải được tệp.");
  });
});

describe("fieldReasonMessage", () => {
  it.each([
    ["must_be_positive", "Phải lớn hơn 0."],
    ["before_period_from", "Đến ngày phải sau hoặc bằng từ ngày."],
    ["in_future", "Không được sau ngày hôm nay."],
    ["exceeds_open", "Vượt số còn nợ."],
    ["not_before_current", "Phải trước ngày đang khóa sổ."],
    ["out_of_range", "Ngoài khoảng cho phép."],
    ["something_new", "Giá trị không hợp lệ."],
  ])("reads %s", (reason, text) => {
    expect(fieldReasonMessage(reason)).toBe(text);
  });
});
