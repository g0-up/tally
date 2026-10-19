import { act, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { TENANT_ID, consoleHandlers, listBody, testSession } from "../testing/fixtures";
import { SUPPORT_VIEW_NO_GRANT } from "./support-view-page";

const PATH = `/admin/support/tenants/${TENANT_ID}/catalog`;

function recordViews() {
  const asked: string[] = [];
  server.use(
    http.get(adminApiUrl("/tenants/:id/support-view/*"), ({ request }) => {
      const url = new URL(request.url);
      asked.push(url.pathname + url.search);
      return HttpResponse.json({ data: [{ sku: "KEO-DUA-500", name: "Kẹo dừa Bến Tre 500g" }], next_cursor: null });
    }),
  );
  return asked;
}

describe("SupportViewPage", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("reads nothing from the store without an active support session", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/support-sessions"), listBody([testSession({ state: "expired" }), testSession({ modules: ["sales"] })])));
    const asked = recordViews();
    renderApp(PATH, "admin");

    expect(await screen.findByText(SUPPORT_VIEW_NO_GRANT)).toBeInTheDocument();
    expect(screen.queryByText(/KEO-DUA-500/)).not.toBeInTheDocument();
    expect(asked).toEqual([]);
  });

  it("shows the store's answer read-only through an active session", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/support-sessions"), listBody([testSession()])));
    const asked = recordViews();
    renderApp(`${PATH}?path=${encodeURIComponent("products?q=keo")}`, "admin");

    expect(await screen.findByText(/"sku": "KEO-DUA-500"/)).toBeInTheDocument();
    expect(asked).toEqual([`/api/admin/v1/tenants/${TENANT_ID}/support-view/catalog/products?q=keo`]);
    expect(screen.queryByRole("button", { name: /Lưu|Ghi sổ/ })).not.toBeInTheDocument();
  });

  it("takes the store's data off the screen when the session runs out", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"], shouldAdvanceTime: true });
    vi.setSystemTime(new Date("2026-10-17T03:00:00Z"));
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/support-sessions"), listBody([testSession({ expires_at: "2026-10-17T04:00:00Z" })])));
    recordViews();
    renderApp(PATH, "admin");
    expect(await screen.findByText(/"sku": "KEO-DUA-500"/)).toBeInTheDocument();

    act(() => vi.advanceTimersByTime(60 * 60_000));

    expect(await screen.findByText(SUPPORT_VIEW_NO_GRANT)).toBeInTheDocument();
    expect(screen.queryByText(/KEO-DUA-500/)).not.toBeInTheDocument();
  });

  it("says so when the owner revoked the session meanwhile", async () => {
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/support-sessions"), listBody([testSession()])),
      http.get(adminApiUrl("/tenants/:id/support-view/*"), () => apiError(403, "support_session_inactive")),
    );
    renderApp(PATH, "admin");
    expect(await screen.findByText(/Phiên hỗ trợ đã hết hạn hoặc đã bị thu hồi/)).toBeInTheDocument();
  });
});
