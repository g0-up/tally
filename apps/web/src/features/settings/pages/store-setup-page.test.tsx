import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { apiError, apiUrl, testOnboarding, withOnboarding } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { STORE_SETUP_LEAD } from "./store-setup-page";

const PATH = "/settings/store-setup";

describe("StoreSetupPage", () => {
  it("lists the seven steps in display order with their type and status", async () => {
    server.use(withOnboarding(testOnboarding({ catalog_setup: "skipped" })));
    renderApp(PATH);

    expect(await screen.findByRole("heading", { level: 1, name: "Khởi tạo cửa hàng" })).toBeInTheDocument();
    expect(screen.getByText(STORE_SETUP_LEAD)).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Tiến độ cửa hàng mới" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Bước", "Loại", "Trạng thái"]);
    const rows = within(table)
      .getAllByRole("row")
      .slice(1)
      .map((r) => within(r).getAllByRole("cell").map((c) => c.textContent));
    expect(rows).toEqual([
      ["1. Thông tin cửa hàng", "Bắt buộc", "Đã xong"],
      ["2. Danh mục hàng hóa", "Tùy chọn", "Đã bỏ qua"],
      ["3. Tồn kho đầu kỳ", "Bắt buộc", "Đã xong"],
      ["4. Công nợ đầu kỳ", "Bắt buộc", "Đã xong"],
      ["5. Tiền đầu kỳ", "Bắt buộc", "Đã xong"],
      ["6. Xác nhận và khóa số dư", "Bắt buộc", "Đã xong"],
      ["7. Bán đơn đầu tiên", "Tùy chọn", "Đã bỏ qua"],
    ]);
    expect(screen.getByText("5/5 bước bắt buộc")).toBeInTheDocument();
  });

  it("opens the onboarding screen", async () => {
    const user = userEvent.setup();
    const { router } = renderApp(PATH);

    await user.click(await screen.findByRole("link", { name: "Mở màn onboarding" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/onboarding"));
    expect(await screen.findByRole("heading", { level: 1, name: "Thiết lập cửa hàng" })).toBeInTheDocument();
  });

  it("offers a retry when the checklist cannot be read", async () => {
    server.use(http.get(apiUrl(PATHS.onboarding), () => apiError(500, "internal")));
    renderApp(PATH);

    expect(await screen.findByRole("button", { name: "Thử lại" })).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Tiến độ cửa hàng mới" })).not.toBeInTheDocument();
  });
});
