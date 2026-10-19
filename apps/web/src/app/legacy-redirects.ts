import { pathForViewId } from "./menu";

// Old links keep working. The prototype addressed screens by hash ("#kho.ton") and moved some of
// them (MOVED); the product uses paths. `LEGACY_HASH_MAP` mirrors the prototype MOVED table
// translated to paths; any other "#mod.sub" hash resolves through the menu.

const CASH_FLOW = "/reports/cash-flow";

/** Prototype MOVED (+ the removed cash module) → product path. */
export const LEGACY_HASH_MAP: Readonly<Record<string, string>> = {
  "#thiet-lap.nha-cung-cap": "/catalog/suppliers",
  "#danh-muc.doi-tac": "/campaigns/customers",
  "#thiet-lap.tich-hop": "/settings/account",
  "#thiet-lap.du-lieu": "/settings/account",
  "#kho.mau": "/inventory/stock",
  "#tien.quy": CASH_FLOW,
  "#tien.thu-chi": CASH_FLOW,
  "#tien.chuyen-tien": CASH_FLOW,
  "#tien.sao-ke": CASH_FLOW,
  "#tien.ca": CASH_FLOW,
  "#tien.chi-phi": CASH_FLOW,
};

/** Paths that moved in the product itself. */
export const MOVED_PATHS: Readonly<Record<string, string>> = {
  "/settings/suppliers": "/catalog/suppliers",
};

/**
 * Target path for a legacy hash ("#kho.ton", "#tien.quy"), or null when the hash is not a legacy
 * screen address (in-page anchors stay untouched).
 */
export function resolveLegacyHash(hash: string): string | null {
  if (!hash.startsWith("#") || hash.length < 2) return null;
  const moved = LEGACY_HASH_MAP[hash];
  if (moved) return moved;
  const viewId = hash.slice(1);
  if (!/^[a-z-]+(\.[a-z-]*)?$/.test(viewId)) return null;
  return pathForViewId(viewId);
}

/** Where the app should go on first load at `pathname` + `hash`; null = stay. */
export function legacyRedirectTarget(pathname: string, hash: string): string | null {
  if (pathname !== "/" && pathname !== "") return null;
  return resolveLegacyHash(hash);
}
