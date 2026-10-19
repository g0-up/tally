import { describe, expect, it } from "vitest";
import { ApiError, apiErrorFromBody, unknownErrorMessage } from "../../../shared/api/api-error";
import { REPORT_MESSAGES, formatCell, reportErrorMessage, seasonStart } from "./adapters";

const refusal = (status: number, code: string, fields: Record<string, string> = {}) => new ApiError({ status, code, message: code, fields });

describe("formatCell", () => {
  it("formats the server's figures by column kind and leaves text alone", () => {
    expect(formatCell("money", "1250000")).toMatch(/^1\.250\.000\s₫$/);
    expect(formatCell("qty", "1500.5")).toBe("1.500,5");
    expect(formatCell("int", "12")).toBe("12");
    expect(formatCell("percent", "25.75")).toBe("25,75%");
    expect(formatCell("date", "2026-10-05")).toBe("05/10/2026");
    expect(formatCell("text", "1250000")).toBe("1250000");
  });

  it("shows a dash for an empty cell and the raw text for a value that is not a number", () => {
    expect(formatCell("money", null)).toBe("—");
    expect(formatCell("qty", "")).toBe("—");
    expect(formatCell("money", "n/a")).toBe("n/a");
  });
});

describe("seasonStart", () => {
  it("starts the season on 01/10 of this year from October, of last year before", () => {
    expect(seasonStart("2026-10-12")).toBe("2026-10-01");
    expect(seasonStart("2026-12-31")).toBe("2026-10-01");
    expect(seasonStart("2027-02-03")).toBe("2026-10-01");
    expect(seasonStart("2026-10-01")).toBe("2026-10-01");
    expect(seasonStart("2026-09-30")).toBe("2025-10-01");
  });
});

describe("reportErrorMessage", () => {
  it("maps the reporting refusals onto the screens' sentences", () => {
    expect(reportErrorMessage(refusal(422, "range_too_long"))).toBe(REPORT_MESSAGES.rangeTooLong);
    expect(reportErrorMessage(refusal(422, "validation_failed", { to: "before_from" }))).toBe(REPORT_MESSAGES.periodInvalid);
    expect(reportErrorMessage(refusal(409, "saved_report_name_taken"))).toBe(REPORT_MESSAGES.nameTaken);
    expect(reportErrorMessage(refusal(409, "export_queue_full"))).toBe(REPORT_MESSAGES.queueFull);
    expect(reportErrorMessage(refusal(410, "export_expired"))).toBe(REPORT_MESSAGES.exportExpired);
    expect(reportErrorMessage(refusal(409, "export_not_ready"))).toBe(REPORT_MESSAGES.exportNotReady);
    expect(reportErrorMessage(refusal(409, "export_status_invalid"))).toBe(REPORT_MESSAGES.cancelTooLate);
    expect(reportErrorMessage(refusal(404, "report_unknown"))).toBe(REPORT_MESSAGES.reportUnknown);
  });

  it("never shows the server's English for a refusal it does not translate", () => {
    const fromServer = (status: number, code: string) => apiErrorFromBody(status, { error: { code, message: "Something went wrong" } });
    expect(reportErrorMessage(fromServer(422, "invalid_cursor"))).toBe("Danh sách đã thay đổi. Tải lại để xem từ đầu.");
    expect(reportErrorMessage(fromServer(409, "export_new_rule"))).toBe(unknownErrorMessage("export_new_rule"));
    expect(reportErrorMessage(fromServer(409, "export_new_rule"))).not.toContain("Something");
  });
});
