// What a scan resolves to on the screens that take barcodes (POS, stock counts, receipts), and
// the exact code match the POS runs on the goods it already holds. The forms a scanner may report
// for one retail barcode (a UPC with or without leading zeros, UPC-E) are resolved by the server
// lookup only, so a code that matches nothing here goes on to GET /sku-lookup.

/** Where a code came from: the scan box (a USB scanner or typing) or a paired phone. */
export type ScanSource = "keyboard" | "phone";

/** The result of one scan, as a screen reports it back to the scanner. */
export type ScanOutcome =
  | { outcome: "added"; skuId: string; unitId: string }
  | { outcome: "not_found" }
  | { outcome: "needs_desktop_edit" }
  | { outcome: "new_product" }
  | { outcome: "not_in_order" };

export interface BarcodeRef {
  code: string;
  unitId: string;
}

/** A SKU a code may name: its SKU code in its base unit, or one of its barcodes in that barcode's unit. */
export interface CodeCandidate {
  skuCode: string;
  baseUnitId: string;
  barcodes: readonly BarcodeRef[];
}

export interface CodeCandidateMatch<T extends CodeCandidate> {
  item: T;
  unitId: string;
  matchedBy: "barcode" | "sku_code";
}

const sameCode = (a: string, b: string) => a.toUpperCase() === b.toUpperCase();

/**
 * The candidate a code names exactly, ignoring case: a barcode of any candidate first, then a SKU
 * code. Null when the code is blank or names none.
 */
export function matchCode<T extends CodeCandidate>(code: string, items: readonly T[]): CodeCandidateMatch<T> | null {
  const wanted = code.trim();
  if (wanted === "") return null;
  for (const item of items) {
    const barcode = item.barcodes.find((b) => sameCode(b.code, wanted));
    if (barcode) return { item, unitId: barcode.unitId, matchedBy: "barcode" };
  }
  const item = items.find((i) => sameCode(i.skuCode, wanted));
  return item ? { item, unitId: item.baseUnitId, matchedBy: "sku_code" } : null;
}
