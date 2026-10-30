import type { Page, Route } from "@playwright/test";

// Browser-level stand-in for the platform endpoints the shell reads before any screen renders
// (host context, session, store, subscription, entitlements, onboarding, unread count). Shell
// journeys use it while the identity endpoints are not served by the API; journeys that need a
// real sign-in (auth.spec.ts) talk to the API instead. Unknown API paths answer 404 with the API's
// error envelope and are recorded, so a spec can assert the shell asked for nothing else.

export const MOCK_STORE = {
  id: "01J000000000000000000TENANT",
  slug: "demo",
  display_name: "Đại lý Bánh kẹo Tết Hòa Phát",
  business_date: "2026-10-12",
};

export const MOCK_OWNER = { id: "01J0000000000000000000USER", full_name: "Nguyễn Văn An" };

const ONBOARDING_DONE = {
  steps: [
    { key: "store_profile", status: "done" },
    { key: "catalog_setup", status: "done" },
    { key: "opening_stock", status: "done" },
    { key: "opening_debt", status: "done" },
    { key: "opening_cash", status: "done" },
    { key: "first_sale", status: "skipped" },
    { key: "confirm_opening_balances", status: "done" },
  ],
};

export interface MockApiOptions {
  /** Sandbox store: shows the demo pill and the "Đặt lại mẫu" button. */
  sandbox?: boolean;
  /** Unread notifications shown in the util bar. */
  unread?: number;
}

export interface MockApi {
  /** API paths requested that the mock does not serve ("GET /api/v1/…"). */
  unhandled: string[];
}

export async function mockSignedInStore(page: Page, options: MockApiOptions = {}): Promise<MockApi> {
  const sandbox = options.sandbox ?? false;
  const bodies: Record<string, unknown> = {
    "GET /api/v1/host-context": { kind: "tenant", root_url: "http://localhost:5173", csrf_token: "e2e-csrf-token" },
    "GET /api/v1/me": {
      csrf_token: "e2e-csrf-token",
      user: MOCK_OWNER,
      tenant: { id: MOCK_STORE.id, slug: MOCK_STORE.slug, display_name: MOCK_STORE.display_name, is_sandbox: sandbox },
      // The prototype's seed business date (visual/prototype-harness.ts SEED_DATE); /me requires it.
      server_time: "2026-10-12T09:00:00+07:00",
    },
    "GET /api/v1/tenant": { ...MOCK_STORE, is_sandbox: sandbox },
    "GET /api/v1/subscription": { status: "active", plan: "pro" },
    "GET /api/v1/entitlements": { modules: ["pos", "kits", "campaign", "crm"] },
    "GET /api/v1/onboarding": ONBOARDING_DONE,
    "GET /api/v1/notifications/unread-count": { count: options.unread ?? 3 },
  };
  const api: MockApi = { unhandled: [] };

  // Match on the path prefix: a "**/api/**" glob would also catch dev-server modules under src/shared/api/.
  await page.route((url) => url.pathname.startsWith("/api/"), async (route: Route) => {
    const request = route.request();
    const key = `${request.method()} ${new URL(request.url()).pathname}`;
    const body = bodies[key];
    if (body === undefined) {
      api.unhandled.push(key);
      await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "not found" } } });
      return;
    }
    await route.fulfill({ status: 200, json: body });
  });
  return api;
}
