import { describe, expect, it } from "vitest";
import { businessDateOf, isTimeZone, todayBusinessDate } from "./dates";

describe("business dates", () => {
  it("dates an instant in the business time zone, not in UTC", () => {
    expect(businessDateOf("2026-10-12T17:30:00Z")).toBe("2026-10-13");
    expect(businessDateOf("2026-10-12T23:50:00+07:00")).toBe("2026-10-12");
    expect(todayBusinessDate(new Date("2026-10-12T16:59:00Z"))).toBe("2026-10-12");
  });

  it("dates an instant in the store's own time zone when given one", () => {
    expect(businessDateOf("2026-10-12T17:30:00Z", "Asia/Saigon")).toBe("2026-10-13");
    expect(businessDateOf("2026-10-12T17:30:00Z", "UTC")).toBe("2026-10-12");
  });

  it("refuses an instant it cannot read", () => {
    expect(() => businessDateOf("hôm nay")).toThrow(RangeError);
  });

  it("knows the IANA time zones the browser knows", () => {
    expect(isTimeZone("Asia/Ho_Chi_Minh")).toBe(true);
    expect(isTimeZone("Asia/Saigon")).toBe(true);
    expect(isTimeZone("Mars/Olympus")).toBe(false);
  });
});
