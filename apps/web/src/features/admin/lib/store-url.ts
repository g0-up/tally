import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";

/** Address of a store's own host, `https://<slug>.<root domain>`, from the root URL of the host context. */
export function storeUrl(slug: string, rootUrl: string): string | null {
  try {
    const root = new URL(rootUrl);
    return `${root.protocol}//${slug}.${root.host}`;
  } catch {
    return null;
  }
}

const MB = 1024 * 1024;

/** Storage counter in MB with one decimal, the unit plan limits use (storage_mb). */
export function storageText(bytes: number | null): string {
  if (bytes === null) return "—";
  return `${num(dec(bytes).div(MB).toDecimalPlaces(1))} MB`;
}
