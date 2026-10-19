import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../shared/api/endpoints";
import { READ_ONLY_TITLE } from "../../shared/ui/read-only-banner";
import { renderApp, renderWithProviders } from "../../test/render";
import { adminApiUrl, apiError, apiUrl, signedOut, testOnboarding, withEntitlements, withOnboarding } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { consoleHandlers } from "../../features/admin/testing/fixtures";
import { ADMIN_NAV_LABEL } from "../layouts/admin-shell";
import { MODULE_NOT_IN_PLAN } from "./require-entitlement";
import { RECENT_AUTH_TEXT, RequireRecentAuth } from "./require-recent-auth";

describe("RequireAuth", () => {
  it("sends a signed-out visitor to /login with the requested page in next", async () => {
    server.use(...signedOut);
    const { router } = renderApp("/inventory/stock?tab=a");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toBe(`?next=${encodeURIComponent("/inventory/stock?tab=a")}`);
  });

  it("sends the owner to /login with the current page in next when the session expires mid-visit", async () => {
    server.use(http.get(apiUrl("/things"), () => apiError(401, "unauthenticated")));
    const { router, platform } = renderApp("/inventory/stock?tab=a");
    expect(await screen.findByRole("heading", { level: 1, name: "Tồn kho" })).toBeInTheDocument();

    server.use(...signedOut);
    await expect(platform.client.get("/things")).rejects.toMatchObject({ status: 401 });

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toBe(`?next=${encodeURIComponent("/inventory/stock?tab=a")}`);
  });

  it("shows the error with a retry when the session cannot be read", async () => {
    server.use(http.get(apiUrl(PATHS.me), () => apiError(500, "internal", { message: "boom" })));
    renderApp("/inventory/stock");
    expect(await screen.findByRole("alert", {})).toHaveTextContent("Không hiển thị được màn hình");
    expect(screen.getByRole("button", { name: "Thử lại" })).toBeInTheDocument();
  });
});

describe("OnboardingGate", () => {
  it("sends a store with a pending required step to /onboarding", async () => {
    server.use(withOnboarding(testOnboarding({ opening_stock: "pending", confirm_opening_balances: "pending" })));
    const { router } = renderApp("/inventory/stock");
    await waitFor(() => expect(router.state.location.pathname).toBe("/onboarding"));
    expect(await screen.findByRole("heading", { level: 1, name: "Thiết lập cửa hàng" })).toBeInTheDocument();
    expect(await screen.findByRole("navigation", { name: "Các bước thiết lập" })).toBeInTheDocument();
  });

  it("keeps the screens the steps link to open while a required step is pending, and nothing else", async () => {
    server.use(
      withOnboarding(testOnboarding({ catalog_setup: "pending", opening_stock: "pending", confirm_opening_balances: "pending" })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [], next_cursor: null })),
    );
    const user = userEvent.setup();
    const { router } = renderApp("/onboarding");

    await user.click(await screen.findByRole("link", { name: "Mở danh mục đầy đủ" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Hàng hóa" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/catalog/products");

    await router.navigate("/catalog/suppliers");
    expect(await screen.findByRole("heading", { level: 1, name: "Nhà cung cấp" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/catalog/suppliers");

    await router.navigate("/sales");
    await waitFor(() => expect(router.state.location.pathname).toBe("/onboarding"));
    expect(await screen.findByRole("heading", { level: 1, name: "Thiết lập cửa hàng" })).toBeInTheDocument();
  });

  it("lets skippable steps wait", async () => {
    server.use(withOnboarding(testOnboarding({ catalog_setup: "pending", first_sale: "pending" })));
    const { router } = renderApp("/inventory/stock");
    expect(await screen.findByRole("heading", { level: 1, name: "Tồn kho" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/inventory/stock");
  });

  it("keeps the app usable when the checklist cannot be read", async () => {
    server.use(http.get(apiUrl(PATHS.onboarding), () => apiError(500, "internal")));
    renderApp("/inventory/stock");
    expect(await screen.findByRole("heading", { level: 1, name: "Tồn kho" })).toBeInTheDocument();
  });
});

describe("RequireEntitlement", () => {
  it("explains a module missing from the plan and links to the subscription", async () => {
    server.use(withEntitlements({ modules: { pos: false, kits: true } }));
    renderApp("/sales/pos");
    expect(await screen.findByText(MODULE_NOT_IN_PLAN)).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "POS & giỏ hàng" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Xem gói thuê bao" })).toHaveAttribute("href", "/settings/subscription");
  });

  it("renders the page when the plan includes the module", async () => {
    server.use(withEntitlements({ modules: { pos: true } }));
    renderApp("/sales/pos");
    expect(await screen.findByRole("heading", { level: 1, name: "POS & giỏ hàng" })).toBeInTheDocument();
    expect(screen.queryByText(MODULE_NOT_IN_PLAN)).not.toBeInTheDocument();
  });

  it("opens every module of a store without a subscription", async () => {
    server.use(withEntitlements({ status: "", modules: null, limits: null }));
    renderApp("/sales/pos");
    expect(await screen.findByRole("heading", { level: 1, name: "POS & giỏ hàng" })).toBeInTheDocument();
    expect(screen.queryByText(MODULE_NOT_IN_PLAN)).not.toBeInTheDocument();
  });

  it("never gates screens that are in every plan", async () => {
    server.use(withEntitlements({ modules: {} }));
    renderApp("/sales/invoices");
    expect(await screen.findByRole("heading", { level: 1, name: "Hóa đơn" })).toBeInTheDocument();
    expect(screen.queryByText(MODULE_NOT_IN_PLAN)).not.toBeInTheDocument();
  });
});

describe("read-only subscription", () => {
  it.each(["read_only", "closed"] as const)("shows the read-only banner when a %s subscription refuses posting", async (status) => {
    server.use(withEntitlements({ status, can_post: false }));
    renderApp("/inventory/stock");
    expect(await screen.findByText(READ_ONLY_TITLE)).toBeInTheDocument();
  });

  it("shows no banner while a subscription in grace still posts", async () => {
    server.use(withEntitlements({ status: "grace", can_post: true }));
    renderApp("/inventory/stock");
    await screen.findByRole("heading", { level: 1, name: "Tồn kho" });
    expect(screen.queryByText(READ_ONLY_TITLE)).not.toBeInTheDocument();
  });

  it("shows no banner for an active subscription", async () => {
    renderApp("/inventory/stock");
    await screen.findByRole("heading", { level: 1, name: "Tồn kho" });
    expect(screen.queryByText(READ_ONLY_TITLE)).not.toBeInTheDocument();
  });
});

describe("RequireRecentAuth", () => {
  it("hides the section until the password is confirmed", async () => {
    let body: unknown = null;
    server.use(
      http.post(apiUrl(PATHS.reauth), async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <RequireRecentAuth>
        <p>Sensitive section</p>
      </RequireRecentAuth>,
    );
    expect(await screen.findByText(RECENT_AUTH_TEXT)).toBeInTheDocument();
    expect(screen.queryByText("Sensitive section")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Xác nhận mật khẩu" }));
    await user.type(await screen.findByLabelText("Mật khẩu"), "correct horse");
    await user.click(screen.getByRole("button", { name: "Xác nhận" }));
    expect(await screen.findByText("Sensitive section")).toBeInTheDocument();
    expect(body).toEqual({ password: "correct horse" });
  });
});

describe("RequireAdminAuth", () => {
  it("sends a signed-out admin to /admin/login", async () => {
    server.use(...consoleHandlers, http.get(adminApiUrl(PATHS.adminMe), () => apiError(401, "unauthenticated")));
    const { router } = renderApp("/admin/usage", "admin");
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/login"));
    expect(router.state.location.search).toBe(`?next=${encodeURIComponent("/admin/usage")}`);
  });

  it("uses the admin session, not the store session", async () => {
    server.use(...consoleHandlers, http.get(apiUrl(PATHS.me), () => apiError(401, "unauthenticated")));
    const { router } = renderApp("/admin", "admin");
    expect(await screen.findByRole("navigation", { name: ADMIN_NAV_LABEL })).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/tenants"));
  });

  it("sends the admin to /admin/login when the session ends mid-visit", async () => {
    server.use(...consoleHandlers);
    const { router } = renderApp("/admin/usage", "admin");
    expect(await screen.findByRole("navigation", { name: ADMIN_NAV_LABEL })).toBeInTheDocument();

    server.use(http.get(adminApiUrl(PATHS.adminMe), () => apiError(401, "unauthenticated")), http.get(adminApiUrl("/incidents"), () => apiError(401, "unauthenticated")));
    await userEvent.setup().click(screen.getByRole("link", { name: "Sự cố" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/login"));
    expect(router.state.location.search).toBe(`?next=${encodeURIComponent("/admin/incidents")}`);
  });
});
