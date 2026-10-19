import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { ADMIN_ID, COLLEAGUE, STORE_SCOPE, TENANT_ID, consoleHandlers, flagsBody, heldAnswer, listBody, testFlag, testTenant } from "../testing/fixtures";
import { FLAGS_TABLE, FLAG_HISTORY_TITLE } from "./feature-flags-page";

describe("FeatureFlagsPage", () => {
  it("turns a global flag off only after its key is typed, and shows who changed it", async () => {
    const bodies: unknown[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/feature-flags"), flagsBody([testFlag({ enabled: true }), testFlag({ ...STORE_SCOPE, enabled: false })], ["ai_qa.beta"])),
      http.put(adminApiUrl("/feature-flags"), async ({ request }) => {
        const body = (await request.json()) as { key: string; tenant_id: null; enabled: boolean };
        bodies.push(body);
        return HttpResponse.json(testFlag({ ...body, ...(body.tenant_id === null ? {} : STORE_SCOPE), updated_at: "2026-10-18T02:00:00Z" }));
      }),
      http.get(
        adminApiUrl("/feature-flags/:key/changes"),
        listBody([
          { id: "0193a000-0000-7000-8000-0000000000h1", key: "ai_qa.beta", tenant_id: null, tenant_slug: null, tenant_name: null, enabled: true, admin_id: ADMIN_ID, admin_full_name: "Trần Quản Trị", created_at: "2026-10-10T03:00:00Z" },
          { id: "0193a000-0000-7000-8000-0000000000h2", key: "ai_qa.beta", ...STORE_SCOPE, enabled: null, admin_id: COLLEAGUE.id, admin_full_name: COLLEAGUE.full_name, created_at: "2026-10-11T03:00:00Z" },
        ]),
      ),
    );
    renderApp("/admin/feature-flags", "admin");
    const table = await screen.findByRole("table", { name: FLAGS_TABLE });
    const row = within(table).getByRole("row", { name: /ai_qa\.beta/ });
    // The store's own value is named after the store the API names on it.
    expect(await within(row).findByText("hoa-phat: Tắt")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(within(row).getByRole("button", { name: "Tắt ai_qa.beta cho toàn hệ thống" }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Tắt cờ" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/Gõ mã cờ/), "ai_qa.beta");
    await user.click(submit);

    expect(await screen.findByText(/Đã tắt cờ/)).toHaveTextContent("Đã tắt cờ ai_qa.beta cho toàn hệ thống.");
    expect(bodies).toEqual([{ key: "ai_qa.beta", tenant_id: null, enabled: false }]);

    await user.click(within(row).getByRole("button", { name: "Lịch sử ai_qa.beta" }));
    const history = await screen.findByRole("table", { name: `${FLAG_HISTORY_TITLE} ai_qa.beta` });
    const global = within(history).getByRole("row", { name: /Toàn hệ thống/ });
    expect(global).toHaveTextContent("Bật");
    expect(global).toHaveTextContent("Trần Quản Trị");
    const removal = await within(history).findByRole("row", { name: /hoa-phat/ });
    expect(removal).toHaveTextContent("Đã gỡ giá trị riêng");
    expect(removal).toHaveTextContent("Lê Thu Hỗ Trợ");
  });

  it("shows the wait on the flag being switched on and holds the other rows without claiming it", async () => {
    const answer = heldAnswer();
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/feature-flags"), flagsBody([testFlag(), testFlag({ key: "pos.offline" })], ["ai_qa.beta", "pos.offline"])),
      http.put(adminApiUrl("/feature-flags"), async ({ request }) => {
        const body = (await request.json()) as { key: string; tenant_id: null; enabled: boolean };
        await answer.held;
        return HttpResponse.json(testFlag({ ...body, updated_at: "2026-10-18T02:00:00Z" }));
      }),
    );
    renderApp("/admin/feature-flags", "admin");
    const table = await screen.findByRole("table", { name: FLAGS_TABLE });
    await userEvent.setup().click(within(table).getByRole("button", { name: "Bật ai_qa.beta cho toàn hệ thống" }));

    const running = within(table).getByRole("button", { name: "Bật ai_qa.beta cho toàn hệ thống" });
    await waitFor(() => expect(running).toHaveAttribute("aria-busy", "true"));
    expect(running).toHaveTextContent("Đang bật…");
    const other = within(table).getByRole("button", { name: "Bật pos.offline cho toàn hệ thống" });
    expect(other).toBeDisabled();
    expect(other).not.toHaveAttribute("aria-busy");
    expect(other).toHaveTextContent("Bật");

    answer.release();
    expect(await screen.findByText(/Đã bật cờ/)).toHaveTextContent("Đã bật cờ ai_qa.beta cho toàn hệ thống.");
  });

  it("lists every key the server reads and sets one for a store picked by name", async () => {
    const bodies: unknown[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/feature-flags"), flagsBody([testFlag({ enabled: true })], ["reports.export", "ai_qa.beta"])),
      http.get(adminApiUrl("/tenants"), () => HttpResponse.json({ data: [testTenant()], next_cursor: null })),
      http.put(adminApiUrl("/feature-flags"), async ({ request }) => {
        const body = (await request.json()) as { key: string; tenant_id: string; enabled: boolean };
        bodies.push(body);
        return HttpResponse.json(testFlag({ ...body, ...(body.tenant_id === null ? {} : STORE_SCOPE), updated_at: "2026-10-18T02:00:00Z" }));
      }),
    );
    renderApp("/admin/feature-flags", "admin");
    const table = await screen.findByRole("table", { name: FLAGS_TABLE });
    // A key without any value yet still has its row.
    expect(within(table).getByRole("row", { name: /reports\.export/ })).toHaveTextContent("Chưa đặt (tắt)");

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Đặt cờ" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Lưu cờ" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Chọn mã cờ.");

    const key = within(dialog).getByLabelText(/^Mã cờ/);
    expect(within(key).getAllByRole("option").map((o) => o.textContent)).toEqual(["Chọn mã cờ", "ai_qa.beta", "reports.export"]);
    await user.selectOptions(key, "reports.export");
    await user.click(within(dialog).getByRole("combobox", { name: "Phạm vi" }));
    await user.click(await within(dialog).findByRole("option", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ }));
    await user.click(within(dialog).getByRole("button", { name: "Lưu cờ" }));

    expect(await screen.findByText(/Đã bật cờ/)).toHaveTextContent("Đã bật cờ reports.export cho hoa-phat.");
    expect(bodies).toEqual([{ key: "reports.export", tenant_id: TENANT_ID, enabled: true }]);
  });
});
