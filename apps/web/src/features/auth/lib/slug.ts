import { normalizeText } from "../../../shared/lib/textnorm";

// Store address rules, mirrored from the API (org.SlugPolicy) so the form can answer instantly.
// The reserved list lives on the server only (it also reads RESERVED_SLUGS), so "reserved" and
// "taken" always come from GET /signup/slug-check.

/** Live availability line under the store address input. */
export const SLUG_STATUS = {
  checking: "Đang kiểm tra địa chỉ…",
  available: "Địa chỉ này dùng được.",
  checkFailed: "Chưa kiểm tra được địa chỉ. Bạn vẫn có thể tiếp tục, hệ thống sẽ kiểm tra khi tạo cửa hàng.",
} as const;

export const SLUG_MIN_LENGTH = 3;
export const SLUG_MAX_LENGTH = 30;

const SLUG_FORMAT = /^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$/;
/** Demo stores end with this suffix; a real store cannot. */
const SANDBOX_SUFFIX = "-demo";

/** True when `slug` has the shape of a store address (availability not checked). */
export function isSlugFormatValid(slug: string): boolean {
  return SLUG_FORMAT.test(slug) && !slug.includes("--") && !slug.endsWith(SANDBOX_SUFFIX);
}

/**
 * A store address derived from the store name: accents removed ("Đại lý Bánh kẹo" →
 * "dai-ly-banh-keo"), every run of other characters turned into one hyphen, cut to 30 characters.
 * May still be invalid (too short, ends in "-demo"); the live check reports that.
 */
export function suggestSlug(storeName: string): string {
  return normalizeText(storeName)
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+/, "")
    .slice(0, SLUG_MAX_LENGTH)
    .replace(/-+$/, "");
}

/** Host suffix of the store addresses (".tally.vn", ".localhost:5173"), from the root URL the API returned. */
export function storeHostSuffix(rootUrl: string | undefined): string | null {
  if (!rootUrl) return null;
  try {
    return `.${new URL(rootUrl).host}`;
  } catch {
    return null;
  }
}
