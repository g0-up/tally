import type { SupportModule, SupportSession } from "../api/schemas";

// The read-only support view forwards a store API GET route under /api/v1 whose first path segment
// belongs to the module the owner granted (the API's support module table).

export const SUPPORT_MODULE_ROUTES: Record<SupportModule, readonly string[]> = {
  catalog: ["products", "skus", "categories", "colors", "units", "tax-rates", "attributes", "attribute-sets", "mix-groups", "search", "price-lists", "price-changes", "promotions"],
  partners: ["parties", "customers", "customer-groups", "suppliers"],
  inventory: ["stock", "stock-card", "lots", "reservations", "stock-adjustments", "stock-counts", "cost-recalculations"],
  procurement: ["procurement", "purchase-orders", "receipts", "supplier-documents", "purchase-returns", "purchase-price-adjustments"],
  sales: ["pos", "quotes", "invoices", "stock-outs", "sales-documents", "sales-returns", "exchanges"],
  debt_cash: ["debts", "payments", "statements", "funds", "payment-methods"],
  kits: ["kits", "assembly-orders", "disassemblies"],
  campaign: ["campaigns"],
  crm: ["care-tasks", "loyalty"],
  reports: ["reports", "saved-reports"],
  settings: ["store", "tenant", "reasons", "period-locks", "audit-events"],
};

export function isSupportModule(value: string | undefined): value is SupportModule {
  return value !== undefined && Object.hasOwn(SUPPORT_MODULE_ROUTES, value);
}

/** First route of the module, read when the admin has not typed one. */
export function defaultSupportPath(module: SupportModule): string {
  return SUPPORT_MODULE_ROUTES[module][0] ?? "";
}

/**
 * The typed route without leading slashes, or null when it cannot be forwarded: an empty or
 * dot segment, a percent-escape (which could spell a dot segment the check would not see), or a
 * first segment outside the module.
 */
export function supportPathError(module: SupportModule, path: string): string | null {
  const [route = ""] = path.split("?", 1);
  const segments = route.split("/");
  if (route === "" || route.includes("%") || segments.some((s) => s === "" || s === "." || s === "..")) return "Đường dẫn không hợp lệ.";
  if (!SUPPORT_MODULE_ROUTES[module].includes(segments[0] ?? "")) return `Đường dẫn phải bắt đầu bằng một trong: ${SUPPORT_MODULE_ROUTES[module].join(", ")}.`;
  return null;
}

/** The store API URL of a checked support path, each route segment escaped. */
export function supportViewUrl(tenantId: string, module: SupportModule, route: string): string {
  return `/tenants/${encodeURIComponent(tenantId)}/support-view/${module}/${route.split("/").map(encodeURIComponent).join("/")}`;
}

/** Longest delay setTimeout keeps (a 32-bit signed count of milliseconds). */
const MAX_TIMER_MS = 2_147_483_647;

/** Milliseconds from `now` until a grant runs out: 0 once it has, at most what setTimeout keeps. */
export function msUntilExpiry(expiresAt: string, now: number = Date.now()): number {
  return Math.min(MAX_TIMER_MS, Math.max(0, Date.parse(expiresAt) - now));
}

/** The admin's session that lets it read `module` of the store now, if any. */
export function activeGrant(sessions: readonly SupportSession[], tenantId: string, module: SupportModule, now: number = Date.now()): SupportSession | undefined {
  return sessions.find((s) => s.state === "active" && s.tenant_id === tenantId && s.modules.includes(module) && Date.parse(s.expires_at) > now);
}
