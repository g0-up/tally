import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { TENANT_ID, consoleHandlers, heldAnswer, pageBody, testPartner } from "../testing/fixtures";
import { PARTNERS_TABLE } from "./partners-page";

const PARTNER = testPartner();

function withPartners(list: readonly unknown[] = [PARTNER]) {
  server.use(...consoleHandlers);
  server.use(http.get(adminApiUrl("/partners"), pageBody(list)));
}

async function openCreate() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Thêm đối tác" }));
  const dialog = await screen.findByRole("dialog");
  await user.type(within(dialog).getByLabelText(/^Tên đối tác/), "Đại lý Tư vấn Bình Minh");
  await user.type(within(dialog).getByLabelText(/^Mã giới thiệu/), "binhminh");
  await user.type(within(dialog).getByLabelText(/^Hoa hồng/), "12,5");
  return { user, dialog };
}

describe("PartnersPage", () => {
  it("lists partners and opens the stores one of them referred", async () => {
    withPartners();
    server.use(
      http.get(adminApiUrl("/partners/:id/referrals"), () =>
        HttpResponse.json({
          partner: PARTNER,
          data: [{ tenant_id: TENANT_ID, slug: "hoa-phat", display_name: "Đại lý Bánh kẹo Tết Hòa Phát", referred_at: "2026-09-01T02:00:00Z", subscription_status: "active", paid_invoices: "4800000", commission: "480000" }],
        }),
      ),
    );
    renderApp("/admin/partners", "admin");
    const user = userEvent.setup();

    const table = await screen.findByRole("table", { name: PARTNERS_TABLE });
    const row = within(table).getByRole("row", { name: /Công ty Tư vấn Minh An/ });
    expect(row).toHaveTextContent("MINHAN");
    expect(row).toHaveTextContent("10%");
    expect(row).toHaveTextContent("Đang hợp tác");

    await user.click(within(row).getByRole("button", { name: "Xem cửa hàng do Công ty Tư vấn Minh An giới thiệu" }));
    const referrals = await screen.findByRole("table", { name: "Cửa hàng do Công ty Tư vấn Minh An giới thiệu" });
    expect(await within(referrals).findByRole("link", { name: "Đại lý Bánh kẹo Tết Hòa Phát" })).toHaveAttribute("href", `/admin/tenants/${TENANT_ID}`);
  });

  it("names the failure when the partners cannot be read", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/partners"), () => apiError(500, "internal_error")));
    renderApp("/admin/partners", "admin");

    const alert = await screen.findByText(/^Không tải được đối tác giới thiệu:/);
    expect(alert).toHaveTextContent("Máy chủ gặp lỗi. Thử lại sau ít phút.");
    expect(screen.queryByText("Chưa có đối tác giới thiệu nào.")).not.toBeInTheDocument();
  });

  it("adds a partner with an upper-case code and confirms it only once the API stored it", async () => {
    const posts: { body: unknown; idempotencyKey: string | null }[] = [];
    const answer = heldAnswer();
    withPartners([]);
    server.use(
      http.post(adminApiUrl("/partners"), async ({ request }) => {
        posts.push({ body: await request.json(), idempotencyKey: request.headers.get("Idempotency-Key") });
        await answer.held;
        return HttpResponse.json(testPartner({ name: "Đại lý Tư vấn Bình Minh", referral_code: "BINHMINH", commission_percent: "12.5", referrals: 0 }), { status: 201 });
      }),
    );
    renderApp("/admin/partners", "admin");
    const { user, dialog } = await openCreate();
    await user.click(within(dialog).getByRole("button", { name: "Thêm đối tác" }));

    await waitFor(() => expect(posts).toHaveLength(1));
    // The admin API takes no Idempotency-Key; the request goes out without one.
    expect(posts[0]).toEqual({ body: { name: "Đại lý Tư vấn Bình Minh", referral_code: "BINHMINH", commission_percent: "12.5" }, idempotencyKey: null });
    expect(screen.queryByText(/Đã thêm đối tác/)).not.toBeInTheDocument();

    answer.release();
    expect(await screen.findByText(/Đã thêm đối tác/)).toHaveTextContent("Đã thêm đối tác Đại lý Tư vấn Bình Minh.");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the dialog and marks the code when another partner already uses it", async () => {
    withPartners();
    server.use(http.post(adminApiUrl("/partners"), () => apiError(409, "referral_code_taken", { fields: { referral_code: "taken" } })));
    renderApp("/admin/partners", "admin");
    const { user, dialog } = await openCreate();
    await user.click(within(dialog).getByRole("button", { name: "Thêm đối tác" }));

    expect(await within(dialog).findByText("Đã có người dùng.")).toBeInTheDocument();
    const toast = screen.getAllByRole("alert").find((el) => el.textContent?.includes("Chưa ghi sổ."));
    expect(toast).toHaveTextContent("Mã giới thiệu đã được dùng.");
    expect(screen.queryByText(/Đã thêm đối tác/)).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
