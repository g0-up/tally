import { describe, expect, it } from "vitest";
import { LEGACY_HASH_MAP, legacyRedirectTarget, resolveLegacyHash } from "./legacy-redirects";
import { MENU_ROUTES } from "./menu";

describe("legacy links", () => {
  it("maps every prototype view id to its path", () => {
    for (const r of MENU_ROUTES) expect(resolveLegacyHash(`#${r.viewId}`)).toBe(r.path);
  });

  it("follows the moved screens", () => {
    expect(resolveLegacyHash("#thiet-lap.nha-cung-cap")).toBe("/catalog/suppliers");
    expect(resolveLegacyHash("#tien.quy")).toBe("/reports/cash-flow");
    for (const target of Object.values(LEGACY_HASH_MAP)) expect(MENU_ROUTES.some((r) => r.path === target)).toBe(true);
  });

  it("sends an unknown sub to the first screen of its module", () => {
    expect(resolveLegacyHash("#kho.khong-co")).toBe("/inventory/stock");
    expect(resolveLegacyHash("#kho")).toBe("/inventory/stock");
  });

  it("ignores anchors that are not screen addresses", () => {
    expect(resolveLegacyHash("")).toBeNull();
    expect(resolveLegacyHash("#")).toBeNull();
    expect(resolveLegacyHash("#khong-ton-tai.x")).toBeNull();
    expect(resolveLegacyHash("#<script>")).toBeNull();
  });

  it("only redirects from the app root", () => {
    expect(legacyRedirectTarget("/", "#kho.ton")).toBe("/inventory/stock");
    expect(legacyRedirectTarget("/inventory/counts", "#kho.ton")).toBeNull();
  });
});
