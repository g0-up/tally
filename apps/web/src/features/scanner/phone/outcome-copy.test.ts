import { describe, expect, it } from "vitest";
import { ApiError, networkError } from "../../../shared/api/api-error";
import { ADDED_FALLBACK_TEXT, INVALID_CODE_TEXT, SEND_NETWORK_TEXT, outcomeCopy, scanFailureCopy, type PhoneScanResult } from "./outcome-copy";

describe("outcomeCopy", () => {
  it("names the product and unit the desktop added", () => {
    expect(outcomeCopy({ outcome: "added", product_name: "Mứt gừng", unit_name: "Hộp" })).toEqual({ text: "Mứt gừng · Hộp", tone: "ok" });
  });

  it("falls back to a plain sentence when an added line comes without names", () => {
    expect(outcomeCopy({ outcome: "added" })).toEqual({ text: ADDED_FALLBACK_TEXT, tone: "ok" });
    expect(outcomeCopy({ outcome: "added", product_name: "  ", unit_name: "Thùng" })).toEqual({ text: "Thùng", tone: "ok" });
  });

  it.each<[PhoneScanResult["outcome"], string, "ok" | "error"]>([
    ["not_found", "Không có trong danh mục", "error"],
    ["not_accepting", "Màn hình trên máy tính không nhận mã quét", "error"],
    ["needs_desktop_edit", "Dòng này nhập theo thùng, sửa số lượng trên máy tính", "error"],
    ["new_product", "Hàng mới, chưa có trong danh mục. Nhập thông tin trên máy tính.", "ok"],
    ["not_in_order", "Hàng này không có trong đơn nhập.", "error"],
    ["desktop_unavailable", "Máy tính chưa sẵn sàng. Kiểm tra màn hình trên máy tính.", "error"],
    ["timeout", "Máy tính chưa phản hồi. Kiểm tra màn hình máy tính trước khi quét lại.", "error"],
  ])("says %s as the sentence the plan fixes", (outcome, text, tone) => {
    expect(outcomeCopy({ outcome })).toEqual({ text, tone });
  });
});

describe("scanFailureCopy", () => {
  it("asks to check the network when no answer came", () => {
    expect(scanFailureCopy(networkError())).toEqual({ text: SEND_NETWORK_TEXT, tone: "error" });
  });

  it("names the wait when scans come too fast", () => {
    const e = new ApiError({ status: 429, code: "rate_limited", message: "slow down", retryAfterSeconds: 2, fromServer: true });
    expect(scanFailureCopy(e).text).toBe("Quét quá nhanh. Thử lại sau 2 giây.");
  });

  it("asks to scan again when the server refused the code itself", () => {
    const e = new ApiError({ status: 422, code: "validation_failed", message: "bad code", fromServer: true });
    expect(scanFailureCopy(e)).toEqual({ text: INVALID_CODE_TEXT, tone: "error" });
  });

  it("translates other refusals instead of showing the server's English", () => {
    const e = new ApiError({ status: 423, code: "tenant_maintenance", message: "maintenance", fromServer: true });
    expect(scanFailureCopy(e).text).toBe("Không gửi được mã: Cửa hàng đang bảo trì. Thử lại sau ít phút.");
  });
});
