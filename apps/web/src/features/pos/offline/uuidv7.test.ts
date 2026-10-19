import { describe, expect, it } from "vitest";
import { isUuidv7, uuidv7, uuidv7Time } from "./uuidv7";

describe("uuidv7", () => {
  it("carries the millisecond time, version 7 and the RFC variant", () => {
    const now = Date.parse("2026-10-18T09:30:00.123Z");
    const id = uuidv7(now, (b) => b.fill(0xff));
    expect(isUuidv7(id)).toBe(true);
    expect(uuidv7Time(id)).toBe(now);
    expect(id.slice(14, 15)).toBe("7");
    expect(id).toBe(`${now.toString(16).padStart(12, "0").slice(0, 8)}-${now.toString(16).padStart(12, "0").slice(8)}-7fff-bfff-ffffffffffff`);
  });

  it("sorts in creation order and differs within the same millisecond", () => {
    const a = uuidv7(1_000);
    const b = uuidv7(2_000);
    expect(a < b).toBe(true);
    expect(uuidv7(5_000)).not.toBe(uuidv7(5_000));
  });

  it("rejects other UUID versions", () => {
    expect(isUuidv7("0192a000-0000-4000-8000-000000000001")).toBe(false);
    expect(isUuidv7("not-a-uuid")).toBe(false);
  });
});
