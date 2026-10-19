import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { TEST_ADMIN_ME, adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { consoleHandlers, heldAnswer, incidentsBody, testIncident } from "../testing/fixtures";
import type { Incident } from "../api/schemas";
import { BANNER_TITLE } from "./incidents-page";

const INCIDENT = testIncident();

const NO_BANNER = "Không có sự cố đang mở; cửa hàng không thấy thông báo nào.";

/** Serves the incidents and records the `status` of every read ("" when absent). */
function withIncidents(list: readonly Incident[] = [INCIDENT]) {
  const statuses: string[] = [];
  const answer = incidentsBody(list);
  server.use(...consoleHandlers);
  server.use(
    http.get(adminApiUrl("/incidents"), ({ request }) => {
      statuses.push(new URL(request.url).searchParams.get("status") ?? "");
      return answer({ request });
    }),
  );
  return statuses;
}

describe("IncidentsPage", () => {
  it("shows an unresolved incident in the store banner with its latest update", async () => {
    const resolved = testIncident({ id: "0193a000-0000-7000-8000-0000000000c2", title: "Mất kết nối máy in", status: "resolved", resolved_at: "2026-10-16T05:00:00Z" });
    const statuses = withIncidents([INCIDENT, resolved]);
    renderApp("/admin/incidents", "admin");

    const banner = (await screen.findByText(BANNER_TITLE)).closest(".card") as HTMLElement;
    expect(await within(banner).findByText("Chậm khi ghi sổ hóa đơn: Đang kiểm tra máy chủ ghi sổ.")).toBeInTheDocument();
    expect(banner).not.toHaveTextContent("Mất kết nối máy in");
    expect([...statuses].sort()).toEqual(["", "open"]);
    expect(screen.getByRole("button", { name: "Cập nhật Chậm khi ghi sổ hóa đơn" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cập nhật Mất kết nối máy in" })).toBeInTheDocument();
    expect(screen.getAllByText("Đang điều tra").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Đã khắc phục").length).toBeGreaterThan(0);
  });

  it("takes the banner from the API's open incidents, whatever pages of the list are loaded", async () => {
    const resolved = testIncident({ id: "0193a000-0000-7000-8000-0000000000c2", title: "Mất kết nối máy in", status: "resolved", resolved_at: "2026-10-16T05:00:00Z" });
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/incidents"), ({ request }) =>
        // The newest page of all incidents holds only a resolved one; the open one is further back.
        new URL(request.url).searchParams.get("status") === "open"
          ? HttpResponse.json({ data: [INCIDENT], next_cursor: null })
          : HttpResponse.json({ data: [resolved], next_cursor: "c2" }),
      ),
    );
    renderApp("/admin/incidents", "admin");

    const banner = (await screen.findByText(BANNER_TITLE)).closest(".card") as HTMLElement;
    expect(await within(banner).findByText("Chậm khi ghi sổ hóa đơn: Đang kiểm tra máy chủ ghi sổ.")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Cập nhật Mất kết nối máy in" })).toBeInTheDocument();
    expect(screen.queryByText(/đã tải/)).not.toBeInTheDocument();
  });

  it("says stores see no banner when the API has no open incident", async () => {
    withIncidents([testIncident({ status: "resolved", resolved_at: "2026-10-16T05:00:00Z" })]);
    renderApp("/admin/incidents", "admin");

    const banner = (await screen.findByText(BANNER_TITLE)).closest(".card") as HTMLElement;
    expect(await within(banner).findByText(NO_BANNER)).toBeInTheDocument();
  });

  it("names the failure when the incidents cannot be read", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/incidents"), () => apiError(500, "internal_error")));
    renderApp("/admin/incidents", "admin");

    const alert = await screen.findByText(/^Không tải được sự cố:/);
    expect(alert).toHaveTextContent("Máy chủ gặp lỗi. Thử lại sau ít phút.");
    expect(await screen.findByText(/^Không tải được sự cố đang mở:/)).toBeInTheDocument();
    // A failed read does not tell the admin that stores see no banner.
    expect(screen.queryByText(NO_BANNER)).not.toBeInTheDocument();
    expect(screen.queryByText("Chưa có sự cố nào.")).not.toBeInTheDocument();
  });

  it("publishes an incident and confirms it only once the API stored it", async () => {
    const posts: { body: unknown; idempotencyKey: string | null; csrf: string | null }[] = [];
    const answer = heldAnswer();
    withIncidents([]);
    server.use(
      http.post(adminApiUrl("/incidents"), async ({ request }) => {
        posts.push({ body: await request.json(), idempotencyKey: request.headers.get("Idempotency-Key"), csrf: request.headers.get("X-CSRF-Token") });
        await answer.held;
        return HttpResponse.json(testIncident({ title: "Chậm đồng bộ tồn kho", impact: "major" }), { status: 201 });
      }),
    );
    renderApp("/admin/incidents", "admin");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Công bố sự cố" }));

    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Công bố" }));
    expect(within(dialog).getByText("Nhập tiêu đề sự cố.")).toBeInTheDocument();
    expect(within(dialog).getByText("Nhập thông báo đầu tiên.")).toBeInTheDocument();
    expect(posts).toEqual([]);

    await user.type(within(dialog).getByLabelText(/^Tiêu đề/), " Chậm đồng bộ tồn kho ");
    await user.selectOptions(within(dialog).getByLabelText("Mức ảnh hưởng"), "major");
    await user.type(within(dialog).getByLabelText(/^Thông báo/), "Đang kiểm tra hàng đợi đồng bộ.");
    await user.click(within(dialog).getByRole("button", { name: "Công bố" }));

    await waitFor(() => expect(posts).toHaveLength(1));
    // The admin API takes no Idempotency-Key; the CSRF token is the one GET /me answered.
    expect(posts[0]).toEqual({
      body: { title: "Chậm đồng bộ tồn kho", impact: "major", status: "investigating", message: "Đang kiểm tra hàng đợi đồng bộ." },
      idempotencyKey: null,
      csrf: TEST_ADMIN_ME.csrf_token,
    });
    expect(screen.queryByText(/Đã công bố sự cố/)).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    answer.release();
    expect(await screen.findByText(/Đã công bố sự cố/)).toHaveTextContent("Đã công bố sự cố Chậm đồng bộ tồn kho.");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the update dialog and names the refusal when the incident is gone", async () => {
    withIncidents();
    server.use(http.patch(adminApiUrl("/incidents/:id"), () => apiError(404, "not_found")));
    renderApp("/admin/incidents", "admin");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Cập nhật Chậm khi ghi sổ hóa đơn" }));

    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Trạng thái"), "resolved");
    await user.type(within(dialog).getByLabelText(/^Thông báo mới/), "Đã khắc phục.");
    await user.click(within(dialog).getByRole("button", { name: "Cập nhật" }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent("Chưa ghi sổ.");
    expect(toast).toHaveTextContent("Không tìm thấy dữ liệu, có thể đã bị xóa.");
    expect(screen.queryByText(/Đã cập nhật sự cố/)).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
