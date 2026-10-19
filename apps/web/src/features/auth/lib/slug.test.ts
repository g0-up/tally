import { describe, expect, it } from "vitest";
import { isSlugFormatValid, storeHostSuffix, suggestSlug } from "./slug";

describe("isSlugFormatValid", () => {
  it.each(["abc", "cua-hang-a", "a1-b2", "x".repeat(30)])("accepts %s", (slug) => {
    expect(isSlugFormatValid(slug)).toBe(true);
  });

  it.each([
    ["too short", "ab"],
    ["too long", "x".repeat(31)],
    ["upper case", "Cua-hang"],
    ["leading hyphen", "-abc"],
    ["trailing hyphen", "abc-"],
    ["double hyphen", "cua--hang"],
    ["sandbox suffix", "cua-hang-demo"],
    ["accented", "cửa-hàng"],
    ["underscore", "cua_hang"],
  ])("rejects %s", (_label, slug) => {
    expect(isSlugFormatValid(slug)).toBe(false);
  });
});

describe("suggestSlug", () => {
  it("removes accents and joins words with single hyphens", () => {
    expect(suggestSlug("Đại lý Bánh kẹo  Tết — Hòa Phát!")).toBe("dai-ly-banh-keo-tet-hoa-phat");
  });

  it("cuts to 30 characters without a trailing hyphen", () => {
    const slug = suggestSlug("Cửa hàng tạp hóa và đồ gia dụng Minh Khai");
    expect(slug.length).toBeLessThanOrEqual(30);
    expect(slug.endsWith("-")).toBe(false);
    expect(slug.startsWith("cua-hang-tap-hoa")).toBe(true);
  });

  it("returns an empty string when nothing usable is left", () => {
    expect(suggestSlug("  !!! ")).toBe("");
  });
});

describe("storeHostSuffix", () => {
  it("uses the host of the root URL, port included", () => {
    expect(storeHostSuffix("http://localhost:5173")).toBe(".localhost:5173");
    expect(storeHostSuffix("https://tally.vn")).toBe(".tally.vn");
  });

  it("is null while the root URL is unknown or unreadable", () => {
    expect(storeHostSuffix(undefined)).toBeNull();
    expect(storeHostSuffix("not a url")).toBeNull();
  });
});
