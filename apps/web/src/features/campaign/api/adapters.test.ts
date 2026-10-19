import { describe, expect, it } from "vitest";
import { ApiError, apiErrorFromBody, errorMessage, unknownErrorMessage } from "../../../shared/api/api-error";
import { demandRowSchema, seasonReportSchema } from "../schemas";
import { testDemandRow, testSeasonReport } from "../testing/fixtures";
import { demandLine, seasonDays, seasonFigure, withCampaignMessage } from "./adapters";

describe("seasonDays", () => {
  it("counts the days gone by within the season, both ends included", () => {
    expect(seasonDays("2026-10-01", "2027-02-28", "2026-10-12")).toEqual({ elapsed: 12, total: 151 });
  });

  it("stays within 0 and the season length before and after it", () => {
    expect(seasonDays("2027-05-01", "2027-07-31", "2026-10-12")).toEqual({ elapsed: 0, total: 92 });
    expect(seasonDays("2026-01-01", "2026-01-31", "2026-10-12")).toEqual({ elapsed: 31, total: 31 });
  });
});

describe("seasonFigure", () => {
  const report = seasonReportSchema.parse(testSeasonReport({ revenue_achieved: null, budget: "" }));

  it("reads a summary figure by key as a decimal", () => {
    expect(seasonFigure(report, "net_revenue")?.toString()).toBe("120000000");
  });

  it("is null for a missing key, a null value or a non-number", () => {
    expect(seasonFigure(report, "expenses")).toBeNull();
    expect(seasonFigure(report, "revenue_achieved")).toBeNull();
    expect(seasonFigure(report, "budget")).toBeNull();
    expect(seasonFigure(undefined, "net_revenue")).toBeNull();
  });
});

describe("demandLine", () => {
  it("sums the projected demand and receipts and finds the first receipt day", () => {
    const line = demandLine(demandRowSchema.parse(testDemandRow()));
    expect(line.openDemand.toString()).toBe("180");
    expect(line.incoming.toString()).toBe("50");
    expect(line.firstIncomingOn).toBe("2026-10-15");
  });

  it("has no receipt day when nothing is expected", () => {
    const line = demandLine(demandRowSchema.parse(testDemandRow({ days: [{ date: "2026-10-01", demand: "5", incoming: "0", projected: "0", shortage: "0" }] })));
    expect(line.incoming.toString()).toBe("0");
    expect(line.firstIncomingOn).toBeNull();
  });
});

describe("withCampaignMessage", () => {
  it("puts the screen's sentence on a known refusal and keeps others", () => {
    const known = withCampaignMessage(new ApiError({ status: 409, code: "campaign_status_invalid", message: "Campaign status changed", fields: {} }));
    expect(known).toBeInstanceOf(ApiError);
    expect((known as ApiError).message).toBe("Trạng thái chiến dịch đã thay đổi. Tải lại rồi thử lại.");
    const other = new ApiError({ status: 409, code: "something_else", message: "x", fields: {} });
    expect(withCampaignMessage(other)).toBe(other);
  });
});

describe("campaign refusals", () => {
  // Every code the server's campaign module answers with.
  it.each([
    ["campaign_status_invalid", 409, "Trạng thái chiến dịch đã thay đổi. Tải lại rồi thử lại."],
    ["campaign_closed", 409, "Chiến dịch đã kết thúc, không sửa checklist được."],
    ["campaign_name_taken", 409, "Đã có chiến dịch khác cùng tên. Đặt tên khác."],
    ["attribution_changed", 409, "Chứng từ vừa được gắn chiến dịch ở nơi khác. Thử lại."],
    ["not_found", 404, "Không tìm thấy dữ liệu, có thể đã bị xóa. Tải lại để xem bản mới nhất."],
    ["invalid_cursor", 422, "Danh sách đã thay đổi. Tải lại để xem từ đầu."],
    ["validation_failed", 422, "Kiểm tra lại các ô được đánh dấu."],
  ])("reads %s in Vietnamese", (code, status, text) => {
    const e = withCampaignMessage(apiErrorFromBody(status, { error: { code, message: "English text" } }));
    expect(errorMessage(e)).toBe(text);
    expect(errorMessage(e)).not.toBe(unknownErrorMessage(code));
  });

  it("keeps the wait the server asked for", () => {
    const e = withCampaignMessage(apiErrorFromBody(409, { error: { code: "campaign_closed", message: "Closed" } }, 20));
    expect((e as ApiError).retryAfterSeconds).toBe(20);
    expect((e as ApiError).code).toBe("campaign_closed");
  });
});
