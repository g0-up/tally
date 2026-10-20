import { describe, expect, it } from "vitest";
import { RECONNECT_DELAYS_MS, reconnectDelay } from "./reconnect";

describe("reconnectDelay", () => {
  it("waits the exact step when the random draw is in the middle", () => {
    expect(RECONNECT_DELAYS_MS.map((_, i) => reconnectDelay(i, () => 0.5))).toEqual([1_000, 2_000, 5_000, 10_000, 30_000]);
  });

  it("moves each wait by at most a fifth either way", () => {
    expect(reconnectDelay(0, () => 0)).toBe(800);
    expect(reconnectDelay(0, () => 0.999_999)).toBe(1_200);
    expect(reconnectDelay(2, () => 0.25)).toBe(4_500);
  });

  it("keeps the last step for every later attempt and the first for a negative one", () => {
    expect(reconnectDelay(12, () => 0.5)).toBe(30_000);
    expect(reconnectDelay(-1, () => 0.5)).toBe(1_000);
  });

  it("stays within the bounds when the random source strays outside [0, 1)", () => {
    expect(reconnectDelay(1, () => -3)).toBe(1_600);
    expect(reconnectDelay(1, () => 7)).toBe(2_400);
  });
});
