import { errorMessage } from "../../../shared/api/api-error";
import type { ColorCode } from "../../../shared/domain/color";
import { matchCode, type CodeCandidate, type ScanOutcome, type ScanSource } from "../../../shared/lib/barcode";
import { matchesAllTokens } from "../../../shared/lib/textnorm";
import type { SkuCodeMatch } from "../../catalog";
import type { ColorMaps } from "../../sales/api/queries";
import type { PosTile } from "../../sales/schemas";
import { tileColors } from "../api/adapters";
import type { PosProduct } from "../hooks/use-pos-cart";

// What one scan adds to the POS cart, whether the code came from the scan box or a paired phone.
// The code alone decides: the text in the scan box filters the tiles but never picks the line.

export interface PosScanDeps {
  /** The tiles on screen: the bootstrap's online, the device bundle's offline. */
  tiles: readonly PosTile[];
  /** False when the counter sells offline: no lookup, and only base-unit lines (offline prices are base-unit prices). */
  online: boolean;
  codeOf: ColorMaps["codeOf"];
  lookup: (code: string) => Promise<SkuCodeMatch | null>;
  add: (product: PosProduct) => void;
  /** Tells the cashier why nothing was added. */
  notify: (message: string) => void;
}

/** The cart line of a tile in unitId. */
export function tileProduct(tile: PosTile, unitId: string, codeOf: ColorMaps["codeOf"]): PosProduct {
  return product(tile.sku_id, tile.sku_code, tile.name, tile.variant_label, unitId, tileColors(tile, codeOf));
}

function product(skuId: string, code: string, name: string, variant: string, unitId: string, colors: readonly ColorCode[]): PosProduct {
  return {
    skuId,
    skuLabel: `${code} · ${variant ? `${name} ${variant}` : name}`,
    unitId,
    autoColor: colors.length === 1 ? (colors[0] ?? null) : null,
  };
}

type TileCandidate = CodeCandidate & { tile: PosTile };

/** The longest code the API looks up (the catalog's barcode limit). */
export const MAX_LOOKUP_CODE_LENGTH = 64;
const PRINTABLE_ASCII = /^[\x21-\x7e]+$/;

/**
 * Whether the server could find anything for a trimmed code: printable ASCII without spaces, at
 * most MAX_LOOKUP_CODE_LENGTH characters, the rule GET /sku-lookup checks before it looks (it
 * answers 422 otherwise). Names typed in the scan box ("kẹo dừa") fail it and skip the round trip.
 */
export function canLookUpCode(code: string): boolean {
  return code.length <= MAX_LOOKUP_CODE_LENGTH && PRINTABLE_ASCII.test(code);
}

/**
 * Adds at most one line for code: an exact barcode or SKU code of a tile, then (online, for a code
 * that can be a barcode) the server lookup, then, for the scan box only, the first tile whose code
 * or name holds every word of code.
 */
export async function posScan(code: string, source: ScanSource, deps: PosScanDeps): Promise<ScanOutcome> {
  const wanted = code.trim();
  if (wanted === "") return { outcome: "not_found" };
  const add = (p: PosProduct): ScanOutcome => {
    deps.add(p);
    return { outcome: "added", skuId: p.skuId, unitId: p.unitId };
  };

  // A bundle saved before tiles carried barcodes has none.
  const candidates: TileCandidate[] = deps.tiles.map((tile) => ({
    tile,
    skuCode: tile.sku_code,
    baseUnitId: tile.unit_id,
    barcodes: (tile.barcodes ?? []).map((b) => ({ code: b.code, unitId: b.unit_id })),
  }));
  const local = matchCode(wanted, candidates);
  if (local) {
    const { tile } = local.item;
    if (local.unitId === tile.unit_id || deps.online) return add(tileProduct(tile, local.unitId, deps.codeOf));
    const unit = (tile.barcodes ?? []).find((b) => b.unit_id === local.unitId)?.unit_name ?? "đơn vị khác";
    deps.notify(`Mã này là của ${unit}, cần có mạng để bán theo đơn vị này.`);
    return { outcome: "not_found" };
  }

  let failure: unknown = null;
  if (deps.online && canLookUpCode(wanted)) {
    try {
      const m = await deps.lookup(wanted);
      if (m) {
        const colors = tileColors({ stock: m.sku.on_hand_by_color }, deps.codeOf);
        return add(product(m.sku.id, m.sku.code, m.sku.display_name, m.sku.variant_label, m.unit_id, colors));
      }
    } catch (e) {
      failure = e;
    }
  }

  if (source === "keyboard") {
    const tile = deps.tiles.find((t) => matchesAllTokens(wanted, t.sku_code, t.name, t.variant_label));
    if (tile) return add(tileProduct(tile, tile.unit_id, deps.codeOf));
  }

  deps.notify(failure === null ? `Không tìm thấy mã “${wanted}”.` : `Không tra được mã “${wanted}”: ${errorMessage(failure)}`);
  return { outcome: "not_found" };
}
