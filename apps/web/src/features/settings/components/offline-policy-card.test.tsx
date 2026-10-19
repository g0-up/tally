import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { TENANT_SETTINGS, tenantSettingsHandlers } from "../../pos/offline/testing/handlers";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { OFFLINE_RISK_HINT, OfflinePolicyCard } from "./offline-policy-card";

const ENABLE = "Bán ngoại tuyến tại quầy";
// The label wraps its control, hint and error, so names start with the label text.
const AMOUNT = /^Hạn mức tiền mặt ngoại tuyến/;
const HOURS = /^Số giờ tối đa không đồng bộ/;

describe("OfflinePolicyCard", () => {
  it("shows the store's policy and the risk notice", async () => {
    server.use(...tenantSettingsHandlers().handlers);
    renderWithProviders(<OfflinePolicyCard />);
    expect(await screen.findByRole("checkbox", { name: ENABLE })).not.toBeChecked();
    expect(screen.getByLabelText(AMOUNT)).toHaveValue("2.000.000");
    expect(screen.getByLabelText(HOURS)).toHaveValue("12");
    expect(screen.getByText(OFFLINE_RISK_HINT)).toBeInTheDocument();
  });

  it("turns offline selling on with the caps, under If-Match of the version read", async () => {
    const user = userEvent.setup();
    const settings = tenantSettingsHandlers();
    server.use(...settings.handlers);
    renderWithProviders(<OfflinePolicyCard />);

    await user.click(await screen.findByRole("checkbox", { name: ENABLE }));
    await user.clear(screen.getByLabelText(AMOUNT));
    await user.type(screen.getByLabelText(AMOUNT), "1500000");
    await user.clear(screen.getByLabelText(HOURS));
    await user.type(screen.getByLabelText(HOURS), "24");
    await user.click(screen.getByRole("button", { name: "Lưu" }));

    expect(await screen.findByText("Đã lưu chính sách bán ngoại tuyến. Máy bán hàng tải lại dữ liệu khi mở POS.")).toBeInTheDocument();
    expect(settings.patches).toEqual([{ body: { offline_pos_enabled: true, offline_pos_max_amount: "1500000", offline_pos_max_hours: 24 }, ifMatch: `"${TENANT_SETTINGS.version}"` }]);
  });

  it("announces turning it off", async () => {
    const user = userEvent.setup();
    server.use(...tenantSettingsHandlers({ ...TENANT_SETTINGS, offline_pos_enabled: true }).handlers);
    renderWithProviders(<OfflinePolicyCard />);
    await user.click(await screen.findByRole("checkbox", { name: ENABLE }));
    await user.click(screen.getByRole("button", { name: "Lưu" }));
    expect(await screen.findByText("Đã tắt bán ngoại tuyến.")).toBeInTheDocument();
  });

  it("refuses hours outside 1–72 and an empty cap without calling the API", async () => {
    const user = userEvent.setup();
    const settings = tenantSettingsHandlers();
    server.use(...settings.handlers);
    renderWithProviders(<OfflinePolicyCard />);

    await user.clear(await screen.findByLabelText(HOURS));
    await user.type(screen.getByLabelText(HOURS), "0");
    await user.clear(screen.getByLabelText(AMOUNT));
    await user.click(screen.getByRole("button", { name: "Lưu" }));
    expect(screen.getByText("Nhập số giờ nguyên từ 1 đến 72.")).toBeInTheDocument();
    expect(screen.getByText("Nhập hạn mức từ 0 đ trở lên.")).toBeInTheDocument();

    await user.clear(screen.getByLabelText(HOURS));
    await user.type(screen.getByLabelText(HOURS), "73");
    await user.click(screen.getByRole("button", { name: "Lưu" }));
    expect(screen.getByText("Nhập số giờ nguyên từ 1 đến 72.")).toBeInTheDocument();

    await user.clear(screen.getByLabelText(HOURS));
    await user.type(screen.getByLabelText(HOURS), "2.5");
    await user.click(screen.getByRole("button", { name: "Lưu" }));
    expect(screen.getByText("Nhập số giờ nguyên từ 1 đến 72.")).toBeInTheDocument();
    expect(settings.patches).toEqual([]);
  });

  it("needs a cap above 0 đ to turn offline selling on, and accepts 0 đ while it stays off", async () => {
    const user = userEvent.setup();
    const settings = tenantSettingsHandlers();
    server.use(...settings.handlers);
    renderWithProviders(<OfflinePolicyCard />);

    await user.clear(await screen.findByLabelText(AMOUNT));
    await user.type(screen.getByLabelText(AMOUNT), "0");
    await user.click(screen.getByRole("checkbox", { name: ENABLE }));
    await user.click(screen.getByRole("button", { name: "Lưu" }));
    expect(screen.getByText("Bật bán ngoại tuyến cần hạn mức lớn hơn 0 đ.")).toBeInTheDocument();
    expect(settings.patches).toEqual([]);

    await user.click(screen.getByRole("checkbox", { name: ENABLE }));
    await user.click(screen.getByRole("button", { name: "Lưu" }));
    expect(await screen.findByText("Đã tắt bán ngoại tuyến.")).toBeInTheDocument();
    expect(settings.patches.map((p) => p.body)).toEqual([{ offline_pos_enabled: false, offline_pos_max_amount: "0", offline_pos_max_hours: 12 }]);
  });

  it("shows the API's field error next to its field", async () => {
    const user = userEvent.setup();
    // The first matching handler answers, so the refusal goes before the defaults.
    server.use(
      http.patch(apiUrl("/tenant/settings"), () => apiError(422, "validation_failed", { message: "Dữ liệu không hợp lệ.", fields: { offline_pos_max_hours: "lte" } })),
      ...tenantSettingsHandlers().handlers,
    );
    renderWithProviders(<OfflinePolicyCard />);
    await user.click(await screen.findByRole("button", { name: "Lưu" }));
    expect(await screen.findByText("Giá trị quá lớn.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: HOURS })).toHaveAttribute("aria-invalid", "true");
  });

  it("says when the settings cannot be read", async () => {
    server.use(http.get(apiUrl("/tenant/settings"), () => HttpResponse.json({ error: { code: "forbidden", message: "Không có quyền." } }, { status: 403 })));
    renderWithProviders(<OfflinePolicyCard />);
    // The read is retried once before the error shows.
    expect(await screen.findByRole("alert")).toHaveTextContent("Không tải được cài đặt cửa hàng:");
  });
});
