import { describe, expect, it } from "vitest";
import { normalizeVnPhone } from "./phone";

describe("normalizeVnPhone", () => {
  it.each([
    ["0912418226", "0912418226"],
    ["0912 418 226", "0912418226"],
    ["+84 912.418.226", "0912418226"],
    ["+84 0912-418-226", "0912418226"],
    ["0084912418226", "0912418226"],
    ["84912418226", "0912418226"],
    ["(028) 3822 1234", "02838221234"],
    [" 0388888888 ", "0388888888"],
  ])("reads %s as %s", (raw, want) => {
    expect(normalizeVnPhone(raw)).toBe(want);
  });

  it.each([
    ["empty", ""],
    ["too short", "091241822"],
    ["too long", "09124182261"],
    ["unknown prefix", "0112418226"],
    ["landline too short", "0283822123"],
    ["foreign country code", "+1 415 555 0100"],
    ["letters", "0912abc226"],
    ["plus inside", "09+12418226"],
    ["over 30 characters", `0912418226${" ".repeat(25)}1`],
  ])("rejects %s", (_label, raw) => {
    expect(normalizeVnPhone(raw)).toBeNull();
  });
});
