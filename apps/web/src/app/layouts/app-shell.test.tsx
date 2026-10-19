import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PATHS } from "../../shared/api/endpoints";
import { renderApp } from "../../test/render";
import { TEST_ME, TEST_TENANT, apiUrl, withTenant } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { SKIP_LINK_LABEL } from "./app-shell";
import { SANDBOX_FOOTNOTE } from "./brand";
import { OFF_CANVAS_QUERY } from "./sidebar";
import { SANDBOX_PILL, SANDBOX_RESET_DONE, SANDBOX_RESET_TITLE } from "./util-bar";

const sandboxMe = { ...TEST_ME, tenant: { ...TEST_ME.tenant, is_sandbox: true } };

describe("util bar", () => {
  it("shows the store, business date, owner and pending work count", async () => {
    renderApp("/inventory/stock");
    expect(await screen.findByText("Đại lý Bánh kẹo Tết Hòa Phát")).toBeInTheDocument();
    expect(await screen.findByText("12/10/2026")).toBeInTheDocument();
    expect(screen.getByText("Nguyễn Văn An")).toBeInTheDocument();
    const bell = await screen.findByRole("link", { name: "Việc cần xử lý 3" });
    expect(bell).toHaveAttribute("href", "/overview/notifications");
  });

  describe("business date", () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    it("comes from the server's clock when the device is already on the next day", async () => {
      // 00:10 on 13/10 in Hà Nội on the device; the server is still on 12/10.
      vi.useFakeTimers({ toFake: ["Date"] });
      vi.setSystemTime(new Date("2026-10-12T17:10:00Z"));
      server.use(http.get(apiUrl(PATHS.me), () => HttpResponse.json({ ...TEST_ME, server_time: "2026-10-12T23:50:00+07:00" })));
      renderApp("/inventory/stock");
      expect(await screen.findByText("12/10/2026")).toBeInTheDocument();
      expect(screen.queryByText("13/10/2026")).not.toBeInTheDocument();
    });

    it("reads the server's clock in the store's time zone", async () => {
      // 17:30 UTC on 12/10 is already 13/10 in the store; the device says 12/10.
      vi.useFakeTimers({ toFake: ["Date"] });
      vi.setSystemTime(new Date("2026-10-12T10:00:00Z"));
      server.use(
        http.get(apiUrl(PATHS.me), () => HttpResponse.json({ ...TEST_ME, server_time: "2026-10-12T17:30:00Z" })),
        withTenant({ ...TEST_TENANT, timezone: "Asia/Saigon" }),
      );
      renderApp("/inventory/stock");
      expect(await screen.findByText("13/10/2026")).toBeInTheDocument();
      expect(screen.queryByText("12/10/2026")).not.toBeInTheDocument();
    });

    it("follows the time zone the store is set to", async () => {
      // 15:30 UTC is 22:30 on 12/10 in Hà Nội but 00:30 on 13/10 in Tokyo.
      server.use(
        http.get(apiUrl(PATHS.me), () => HttpResponse.json({ ...TEST_ME, server_time: "2026-10-12T15:30:00Z" })),
        withTenant({ ...TEST_TENANT, timezone: "Asia/Tokyo" }),
      );
      renderApp("/inventory/stock");
      expect(await screen.findByText("13/10/2026")).toBeInTheDocument();
    });
  });

  it("shows no sandbox controls for a production store", async () => {
    renderApp("/inventory/stock");
    await screen.findByText("12/10/2026");
    expect(screen.queryByText(SANDBOX_PILL)).not.toBeInTheDocument();
    expect(screen.queryByText(SANDBOX_FOOTNOTE)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Đặt lại mẫu" })).not.toBeInTheDocument();
  });

  it("resets the sandbox store through the API after confirmation", async () => {
    let calls = 0;
    let idempotencyKey: string | null = null;
    server.use(
      http.get(apiUrl(PATHS.me), () => HttpResponse.json(sandboxMe)),
      withTenant({ ...TEST_TENANT, is_sandbox: true }),
      http.post(apiUrl(PATHS.sandboxReset), ({ request }) => {
        calls += 1;
        idempotencyKey = request.headers.get("Idempotency-Key");
        return HttpResponse.json({ ...TEST_TENANT, is_sandbox: true });
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/stock");
    expect(await screen.findByText(SANDBOX_PILL)).toBeInTheDocument();
    expect(screen.getByText(SANDBOX_FOOTNOTE)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Đặt lại mẫu" }));
    const dialog = await screen.findByRole("dialog", { name: SANDBOX_RESET_TITLE });
    expect(calls).toBe(0);
    await user.click(within(dialog).getByRole("button", { name: "Đặt lại" }));
    expect(await screen.findByText(SANDBOX_RESET_DONE)).toBeInTheDocument();
    expect(calls).toBe(1);
    expect(idempotencyKey).toBeNull();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});

describe("sidebar", () => {
  it("expands only the current group and marks the current screen", async () => {
    renderApp("/inventory/counts");
    const nav = await screen.findByRole("navigation", { name: "Menu chính" });
    const stock = within(nav).getByRole("button", { name: "Thu gọn Kho" });
    expect(stock).toHaveAttribute("aria-expanded", "true");
    expect(document.getElementById(stock.getAttribute("aria-controls") ?? "")).toContainElement(
      within(nav).getByRole("link", { name: "Kiểm kê" }),
    );
    expect(within(nav).getByRole("button", { name: "Mở rộng Bán hàng" })).toHaveAttribute("aria-expanded", "false");
    expect(within(nav).getByRole("link", { name: "Kho" })).toHaveAttribute("aria-current", "true");
    expect(within(nav).getByRole("link", { name: "Bán hàng" })).not.toHaveAttribute("aria-current");
    expect(within(nav).getByRole("link", { name: "Kiểm kê" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).queryByRole("link", { name: "Báo giá" })).not.toBeInTheDocument();
  });

  it("folds and unfolds a group from its chevron without leaving the screen", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/inventory/counts");
    const nav = await screen.findByRole("navigation", { name: "Menu chính" });
    await user.click(within(nav).getByRole("button", { name: "Thu gọn Kho" }));
    expect(within(nav).getByRole("button", { name: "Mở rộng Kho" })).toHaveAttribute("aria-expanded", "false");
    expect(within(nav).queryByRole("link", { name: "Kiểm kê" })).not.toBeInTheDocument();
    await user.click(within(nav).getByRole("button", { name: "Mở rộng Bán hàng" }));
    expect(within(nav).getByRole("button", { name: "Thu gọn Bán hàng" })).toHaveAttribute("aria-expanded", "true");
    expect(within(nav).getByRole("link", { name: "Báo giá" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/inventory/counts");
  });

  it("opens another group at its first screen from the group name", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/inventory/counts");
    const nav = await screen.findByRole("navigation", { name: "Menu chính" });
    await user.click(within(nav).getByRole("link", { name: "Bán hàng" }));
    // Pages are lazy chunks: the location moves once the target page has loaded.
    expect(await screen.findByRole("heading", { level: 1, name: "POS & giỏ hàng" }, { timeout: 10_000 })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/sales/pos");
    expect(within(nav).getByRole("link", { name: "Bán hàng" })).toHaveAttribute("aria-current", "true");
    expect(within(nav).getByRole("button", { name: "Mở rộng Kho" })).toHaveAttribute("aria-expanded", "false");
  });

  it("opens from the menu button and closes on Esc and on navigation", async () => {
    const user = userEvent.setup();
    renderApp("/inventory/stock");
    const menu = await screen.findByRole("button", { name: "Mở menu" });
    const side = screen.getByRole("complementary", { name: "Điều hướng" });
    await user.click(menu);
    expect(side).toHaveClass("open");
    expect(menu).toHaveAttribute("aria-expanded", "true");
    await user.keyboard("{Escape}");
    expect(side).not.toHaveClass("open");
    await user.click(menu);
    await user.click(within(side).getByRole("link", { name: "Kiểm kê" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Kiểm kê" })).toBeInTheDocument();
    expect(side).not.toHaveClass("open");
  });
});

describe("off-canvas sidebar", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  /** A phone-width screen, where the sidebar is a sheet over the page. */
  function narrowScreen(): void {
    const real = window.matchMedia.bind(window);
    vi.spyOn(window, "matchMedia").mockImplementation((query) => ({ ...real(query), matches: query === OFF_CANVAS_QUERY }));
  }

  it("keeps the closed sheet out of reach and moves focus in and back out with the menu", async () => {
    narrowScreen();
    const user = userEvent.setup();
    renderApp("/inventory/stock");
    const menu = await screen.findByRole("button", { name: "Mở menu" });
    const side = screen.getByRole("complementary", { name: "Điều hướng" });
    expect(side).toHaveAttribute("inert");

    await user.click(menu);
    expect(side).not.toHaveAttribute("inert");
    expect(side).toContainElement(document.activeElement as HTMLElement);

    await user.keyboard("{Escape}");
    expect(side).toHaveAttribute("inert");
    expect(menu).toHaveFocus();
  });

  it("stays reachable on a wide screen where it is always shown", async () => {
    renderApp("/inventory/stock");
    await screen.findByRole("button", { name: "Mở menu" });
    expect(screen.getByRole("complementary", { name: "Điều hướng" })).not.toHaveAttribute("inert");
  });
});

describe("page landmarks", () => {
  it("skips the menu straight to the screen's main region", async () => {
    const user = userEvent.setup();
    renderApp("/inventory/counts");
    await screen.findByRole("heading", { level: 1, name: "Kiểm kê" });
    await user.tab();
    const skip = screen.getByRole("link", { name: SKIP_LINK_LABEL });
    expect(skip).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(screen.getByRole("main")).toHaveFocus();
  });

  it("names the browser tab after the current screen", async () => {
    document.title = "";
    const user = userEvent.setup();
    renderApp("/inventory/counts");
    await waitFor(() => expect(document.title).toBe("Kiểm kê · Tally"));
    const nav = await screen.findByRole("navigation", { name: "Menu chính" });
    await user.click(within(nav).getByRole("link", { name: "Tồn kho" }));
    await waitFor(() => expect(document.title).toBe("Tồn kho · Tally"));
  });
});
