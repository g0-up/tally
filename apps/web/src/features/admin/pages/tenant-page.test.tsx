import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { REMOVE_OVERRIDE_LABEL, TENANT_FLAGS_TITLE } from "../containers/tenant-flags";
import { STORE_SCOPE, TENANT_ID, consoleHandlers, flagsBody, heldAnswer, testFlag, testTenantDetail } from "../testing/fixtures";

const PATH = `/admin/tenants/${TENANT_ID}`;
const SUBSCRIPTION = testTenantDetail().subscription.subscription;

function withDetail(detail = testTenantDetail()) {
  server.use(...consoleHandlers, http.get(adminApiUrl("/tenants/:id"), () => HttpResponse.json(detail)));
}

const EXPORT_FAILED = testTenantDetail({
  closure: {
    request: { id: "0193a000-0000-7000-8000-0000000000x1", status: "export_failed", verified_at: "2026-10-01T02:00:00Z", export_job_id: "0193a000-0000-7000-8000-0000000000e1", waiting_until: "2026-10-15", legal_hold: false, created_at: "2026-09-30T02:00:00Z" },
    export: { id: "0193a000-0000-7000-8000-0000000000e1", status: "failed", expires_at: null, final: true, failed_attempts: 3 },
  },
});

describe("TenantPage", () => {
  it("suspends a store only after its slug is typed", async () => {
    const bodies: unknown[] = [];
    withDetail();
    server.use(
      http.post(/\/api\/admin\/v1\/tenants\/[^/]+\/subscription:set-status$/, async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ ...SUBSCRIPTION, status: "read_only" });
      }),
    );
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Tạm ngưng" }));

    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Tạm ngưng" });
    expect(submit).toBeDisabled();
    const confirm = within(dialog).getByLabelText(/Gõ địa chỉ cửa hàng/);
    await user.type(confirm, "hoa-pha");
    expect(submit).toBeDisabled();
    await user.type(confirm, "t");
    expect(submit).toBeEnabled();
    await user.click(submit);

    expect(await screen.findByText(/Đã tạm ngưng/)).toHaveTextContent("Đã tạm ngưng hoa-phat: cửa hàng chỉ còn xem.");
    expect(bodies).toEqual([{ status: "read_only" }]);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("shows the failed final export and starts it again", async () => {
    let retried = 0;
    withDetail(EXPORT_FAILED);
    server.use(
      http.post(/\/api\/admin\/v1\/tenants\/[^/]+\/account-closure:retry-export$/, () => {
        retried += 1;
        return HttpResponse.json({ ...EXPORT_FAILED.closure, request: { ...EXPORT_FAILED.closure?.request, status: "exporting" } });
      }),
    );
    renderApp(PATH, "admin");
    expect(await screen.findByText("Xuất dữ liệu lỗi")).toBeInTheDocument();
    expect(screen.getByText("Số lần lỗi").nextElementSibling).toHaveTextContent("3");

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Xuất lại dữ liệu" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("đã lỗi 3 lần");
    await user.click(within(dialog).getByRole("button", { name: "Xuất lại" }));

    expect(await screen.findByText(/Đã bắt đầu xuất lại dữ liệu cuối của/)).toBeInTheDocument();
    expect(retried).toBe(1);
  });

  it("names the refusal when the closure already moved on", async () => {
    withDetail(EXPORT_FAILED);
    server.use(http.post(/\/api\/admin\/v1\/tenants\/[^/]+\/account-closure:retry-export$/, () => apiError(409, "closure_state_invalid")));
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Xuất lại dữ liệu" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Xuất lại" }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent("Chưa ghi sổ.");
    expect(toast).toHaveTextContent("Yêu cầu đóng tài khoản đã chuyển sang bước khác. Tải lại để xem trạng thái mới.");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("turns a flag on for this store over the global value", async () => {
    const bodies: unknown[] = [];
    const answer = heldAnswer();
    withDetail();
    server.use(
      http.get(adminApiUrl("/feature-flags"), flagsBody([testFlag({ enabled: false })], ["ai_qa.beta"])),
      http.put(adminApiUrl("/feature-flags"), async ({ request }) => {
        const body = (await request.json()) as { key: string; tenant_id: string; enabled: boolean };
        bodies.push(body);
        await answer.held;
        return HttpResponse.json(testFlag({ ...body, ...(body.tenant_id === null ? {} : STORE_SCOPE), updated_at: "2026-10-18T02:00:00Z" }));
      }),
    );
    renderApp(PATH, "admin");
    const table = await screen.findByRole("table", { name: TENANT_FLAGS_TITLE });
    const row = within(table).getByRole("row", { name: /ai_qa\.beta/ });
    expect(row).toHaveTextContent("Tắt (theo giá trị chung)");

    await userEvent.setup().click(within(row).getByRole("button", { name: "Bật ai_qa.beta cho hoa-phat" }));
    const running = within(row).getByRole("button", { name: "Bật ai_qa.beta cho hoa-phat" });
    await waitFor(() => expect(running).toHaveAttribute("aria-busy", "true"));
    expect(running).toHaveTextContent("Đang bật…");

    answer.release();
    expect(await screen.findByText(/Đã bật cờ/)).toHaveTextContent("Đã bật cờ ai_qa.beta cho hoa-phat.");
    expect(bodies).toEqual([{ key: "ai_qa.beta", tenant_id: TENANT_ID, enabled: true }]);
  });

  it("removes the store's own value of a flag only after its key is typed", async () => {
    const removed: { url: string; idempotencyKey: string | null }[] = [];
    let stored = [testFlag({ enabled: true }), testFlag({ ...STORE_SCOPE, enabled: false })];
    withDetail();
    server.use(
      http.get(adminApiUrl("/feature-flags"), () => HttpResponse.json({ data: stored, known_keys: ["ai_qa.beta"] })),
      http.delete(adminApiUrl("/tenants/:id/feature-flags/:key"), ({ request }) => {
        removed.push({ url: new URL(request.url).pathname, idempotencyKey: request.headers.get("Idempotency-Key") });
        stored = [testFlag({ enabled: true })];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    const table = await screen.findByRole("table", { name: TENANT_FLAGS_TITLE });
    const row = within(table).getByRole("row", { name: /ai_qa\.beta/ });
    expect(row).toHaveTextContent("Tắt (riêng)");

    await user.click(within(row).getByRole("button", { name: `${REMOVE_OVERRIDE_LABEL} ai_qa.beta cho hoa-phat` }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("hoa-phat sẽ theo giá trị chung của cờ này: Bật.");
    const submit = within(dialog).getByRole("button", { name: REMOVE_OVERRIDE_LABEL });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/Gõ mã cờ/), "ai_qa.beta");
    await user.click(submit);

    expect(await screen.findByText(/Đã gỡ giá trị riêng/)).toHaveTextContent("Đã gỡ giá trị riêng của cờ ai_qa.beta cho hoa-phat.");
    // The admin API takes no Idempotency-Key.
    expect(removed).toEqual([{ url: `/api/admin/v1/tenants/${TENANT_ID}/feature-flags/ai_qa.beta`, idempotencyKey: null }]);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(within(table).getByRole("row", { name: /ai_qa\.beta/ })).toHaveTextContent("Bật (theo giá trị chung)"));
    expect(within(table).queryByRole("button", { name: `${REMOVE_OVERRIDE_LABEL} ai_qa.beta cho hoa-phat` })).not.toBeInTheDocument();
  });

  it("keeps the dialog and says so when the store's own value was already removed", async () => {
    withDetail();
    server.use(
      http.get(adminApiUrl("/feature-flags"), flagsBody([testFlag({ ...STORE_SCOPE, enabled: true })], ["ai_qa.beta"])),
      http.delete(adminApiUrl("/tenants/:id/feature-flags/:key"), () => apiError(404, "flag_override_not_found")),
    );
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    const table = await screen.findByRole("table", { name: TENANT_FLAGS_TITLE });
    await user.click(within(table).getByRole("button", { name: `${REMOVE_OVERRIDE_LABEL} ai_qa.beta cho hoa-phat` }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText(/Gõ mã cờ/), "ai_qa.beta");
    await user.click(within(dialog).getByRole("button", { name: REMOVE_OVERRIDE_LABEL }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent("Cửa hàng không còn giá trị riêng của cờ này, có thể đã được gỡ trước đó.");
    expect(screen.queryByText(/Đã gỡ giá trị riêng của cờ/)).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
