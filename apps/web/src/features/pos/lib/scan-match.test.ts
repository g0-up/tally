import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { createPlatform } from "../../../app/platform";
import type { ScanSource } from "../../../shared/lib/barcode";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { lookupSkuCode } from "../../catalog";
import { testSku } from "../../procurement/testing/fixtures";
import type { PosTile } from "../../sales/schemas";
import { testBundle } from "../offline/testing/handlers";
import { MAX_LOOKUP_CODE_LENGTH, canLookUpCode, posScan, type PosScanDeps } from "./scan-match";

const TILES = testBundle().tiles;

function scanner(init: { online?: boolean; tiles?: readonly PosTile[] } = {}) {
  const add = vi.fn<PosScanDeps["add"]>();
  const notify = vi.fn<PosScanDeps["notify"]>();
  const lookups: string[] = [];
  const client = createPlatform("tenant").client;
  const deps: PosScanDeps = {
    tiles: init.tiles ?? TILES,
    online: init.online ?? true,
    codeOf: () => "none",
    lookup: (code) => {
      lookups.push(code);
      return lookupSkuCode(client, code);
    },
    add,
    notify,
  };
  return { add, notify, lookups, scan: (code: string, source: ScanSource = "keyboard") => posScan(code, source, deps) };
}

/** GET /sku-lookup knowing only code, answering the test SKU in its thùng of 12. */
function lookupServer(code: string) {
  server.use(
    http.get(apiUrl("/sku-lookup"), ({ request }) => {
      const wanted = new URL(request.url).searchParams.get("code");
      if (wanted !== code) return apiError(404, "code_not_found");
      return HttpResponse.json({ sku: testSku(), unit_id: "unit-thung-sp", unit_name: "thùng", factor: "12", matched_by: "barcode" });
    }),
  );
}

describe("posScan", () => {
  it("adds a tile matched by its barcode in the barcode's unit, without asking the server", async () => {
    const s = scanner();
    await expect(s.scan("18935000000021")).resolves.toEqual({ outcome: "added", skuId: "sku-mut", unitId: "unit-thung" });
    expect(s.add).toHaveBeenCalledWith({ skuId: "sku-mut", skuLabel: "MUT-GUNG · Mứt gừng", unitId: "unit-thung", autoColor: null });
    await expect(s.scan("kEO-dua")).resolves.toEqual({ outcome: "added", skuId: "sku-keo", unitId: "unit-goi" });
    expect(s.lookups).toEqual([]);
    expect(s.notify).not.toHaveBeenCalled();
  });

  it("asks the server for a code no tile carries and adds the SKU in the unit it answers", async () => {
    lookupServer("08935001234567");
    const s = scanner();
    await expect(s.scan("08935001234567", "phone")).resolves.toEqual({ outcome: "added", skuId: "sku-1", unitId: "unit-thung-sp" });
    expect(s.add).toHaveBeenCalledWith({ skuId: "sku-1", skuLabel: "SP001 · Bánh quy bơ", unitId: "unit-thung-sp", autoColor: null });
    expect(s.lookups).toEqual(["08935001234567"]);
  });

  it("matches words of a name only for the scan box", async () => {
    lookupServer("nothing-else");
    const s = scanner();
    await expect(s.scan("mứt", "keyboard")).resolves.toEqual({ outcome: "added", skuId: "sku-mut", unitId: "unit-hop" });
    expect(s.add).toHaveBeenCalledTimes(1);
    await expect(s.scan("mứt", "phone")).resolves.toEqual({ outcome: "not_found" });
    expect(s.add).toHaveBeenCalledTimes(1);
    expect(s.notify).toHaveBeenCalledWith("Không tìm thấy mã “mứt”.");
  });

  it("adds nothing when a phone sends a letter that only a tile name holds", async () => {
    lookupServer("nothing-else");
    const s = scanner();
    await expect(s.scan("a", "phone")).resolves.toEqual({ outcome: "not_found" });
    expect(s.add).not.toHaveBeenCalled();
    expect(s.lookups).toEqual(["a"]);
  });

  it("refuses offline a code of another unit than the base unit, and sells the base unit", async () => {
    const s = scanner({ online: false });
    await expect(s.scan("18935000000021")).resolves.toEqual({ outcome: "not_found" });
    expect(s.notify).toHaveBeenCalledWith("Mã này là của Thùng, cần có mạng để bán theo đơn vị này.");
    expect(s.add).not.toHaveBeenCalled();
    await expect(s.scan("8935000000024")).resolves.toEqual({ outcome: "added", skuId: "sku-mut", unitId: "unit-hop" });
    expect(s.lookups).toEqual([]);
  });

  it("does not ask the server offline for a code no tile carries", async () => {
    const s = scanner({ online: false });
    await expect(s.scan("8930000000001", "phone")).resolves.toEqual({ outcome: "not_found" });
    expect(s.lookups).toEqual([]);
    expect(s.notify).toHaveBeenCalledWith("Không tìm thấy mã “8930000000001”.");
  });

  it("matches the SKU codes of a bundle saved before tiles carried barcodes", async () => {
    // Bundles saved by an older build are read back without parsing: their tiles have no barcodes key.
    const old = TILES.map(({ barcodes: _, ...tile }) => tile) as unknown as PosTile[];
    const s = scanner({ online: false, tiles: old });
    await expect(s.scan("MUT-GUNG")).resolves.toEqual({ outcome: "added", skuId: "sku-mut", unitId: "unit-hop" });
    await expect(s.scan("8935000000024", "phone")).resolves.toEqual({ outcome: "not_found" });
  });

  it("says why when the lookup fails, and still tries the words of a name for the scan box", async () => {
    server.use(http.get(apiUrl("/sku-lookup"), () => apiError(500, "internal", { message: "Máy chủ lỗi" })));
    const s = scanner();
    await expect(s.scan("8930000000001", "phone")).resolves.toEqual({ outcome: "not_found" });
    expect(s.notify).toHaveBeenCalledWith(expect.stringMatching(/^Không tra được mã “8930000000001”: /));
    await expect(s.scan("kẹo")).resolves.toEqual({ outcome: "added", skuId: "sku-keo", unitId: "unit-goi" });
  });

  it("goes straight to the words of a name when the text cannot be a barcode", async () => {
    const s = scanner();
    await expect(s.scan("kẹo dừa")).resolves.toEqual({ outcome: "added", skuId: "sku-keo", unitId: "unit-goi" });
    await expect(s.scan("mứt", "phone")).resolves.toEqual({ outcome: "not_found" });
    expect(s.lookups).toEqual([]);
  });

  it("ignores a blank code", async () => {
    const s = scanner();
    await expect(s.scan("   ")).resolves.toEqual({ outcome: "not_found" });
    expect(s.notify).not.toHaveBeenCalled();
    expect(s.lookups).toEqual([]);
  });
});

describe("canLookUpCode", () => {
  it("takes printable ASCII without spaces, up to the API's length", () => {
    expect(canLookUpCode("8935000000024")).toBe(true);
    expect(canLookUpCode("MUT-GUNG/02")).toBe(true);
    expect(canLookUpCode("A".repeat(MAX_LOOKUP_CODE_LENGTH))).toBe(true);
  });

  it("refuses spaces, accents, control characters and codes too long", () => {
    expect(canLookUpCode("keo dua")).toBe(false);
    expect(canLookUpCode("kẹo")).toBe(false);
    expect(canLookUpCode("A\tB")).toBe(false);
    expect(canLookUpCode("A".repeat(MAX_LOOKUP_CODE_LENGTH + 1))).toBe(false);
    expect(canLookUpCode("")).toBe(false);
  });
});
