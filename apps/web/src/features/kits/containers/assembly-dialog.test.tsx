import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { toQty } from "../../../shared/domain/qty";
import { CLOSE_UNSETTLED_MESSAGE, OUTCOME_UNKNOWN_MESSAGE } from "../../../shared/hooks/create-once";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { testSku, UNIT_HOP } from "../../procurement/testing/fixtures";
import { KIT_MESSAGES } from "../api/adapters";
import { testAssemblyDetail, testKit } from "../testing/fixtures";
import { AssemblyDialog } from "./assembly-dialog";

const COLORS = [
  { id: "c-do", code: "do", name: "Đỏ", sort_order: 1, active: true },
  { id: "c-vang", code: "vang", name: "Vàng", sort_order: 2, active: true },
];

/** A published kit of one product and one ribbon; `color` fixes the product's color in the recipe. */
function publishedKit(color: string | null) {
  const recipe = {
    id: "rcp-1",
    version_no: 1,
    status: "active",
    packing_cost: "20000",
    items: [
      { id: "ri-1", line_no: 1, kind: "goods", sku_id: "sku-1", unit_id: UNIT_HOP, qty: "3", base_qty: "3", color_id: color, flavors: [] },
      { id: "ri-2", line_no: 2, kind: "accessory", sku_id: "sku-2", unit_id: UNIT_HOP, qty: "1", base_qty: "1", flavors: [] },
    ],
  };
  return { kit: testKit({ status: "active", active_version: 1, latest: recipe }), recipe };
}

/**
 * The kit's lookups, a product held in red and yellow, and recorders of the preview and create
 * bodies and of the create keys; the first `lost` creates get no answer.
 */
function planServer(color: string | null, lost = 0) {
  const { kit, recipe } = publishedKit(color);
  const previews: unknown[] = [];
  const created: unknown[] = [];
  const keys: Array<string | null> = [];
  server.use(
    http.get(apiUrl("/kits"), () =>
      HttpResponse.json({
        data: [{ product_id: "kit-1", type: "kit", name: "Giỏ An Khang", status: "active", sku_id: "sku-kit", sku_code: "GQAK", latest_version: 1, latest_status: "active", active_version: 1, version: 2 }],
        next_cursor: null,
      }),
    ),
    http.get(apiUrl("/kits/kit-1"), () => HttpResponse.json(kit)),
    http.get(apiUrl("/kits/kit-1/recipes/1"), () => HttpResponse.json(recipe)),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: COLORS })),
    http.get(apiUrl("/invoices"), () => HttpResponse.json({ data: [] })),
    http.get(apiUrl("/skus"), () =>
      HttpResponse.json({
        data: [
          testSku({ on_hand_by_color: [{ color_id: "c-do", qty: toQty(40) }, { color_id: "c-vang", qty: toQty(12) }] }),
          testSku({ id: "sku-2", code: "PK01", display_name: "Nơ đỏ", product_type: "material" }),
        ],
        next_cursor: null,
      }),
    ),
    http.post(/\/assembly-orders:preview$/, async ({ request }) => {
      previews.push(await request.json());
      return HttpResponse.json({ lines: [], buildable: "13", expected_cost: "9000000" });
    }),
    http.post(apiUrl("/assembly-orders"), async ({ request }) => {
      created.push(await request.json());
      keys.push(request.headers.get("Idempotency-Key"));
      if (created.length <= lost) return HttpResponse.error();
      return HttpResponse.json(testAssemblyDetail({ status: "draft", completed_qty: "0" }), { status: 201 });
    }),
  );
  return { previews, created, keys };
}

describe("AssemblyDialog", () => {
  it("asks the color of a product the recipe leaves open, then plans and previews with it", async () => {
    const { previews, created } = planServer(null);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyDialog kitId="kit-1" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
    const swatches = await within(dialog).findByRole("radiogroup", { name: "Màu – Bánh quy bơ" });
    // Only the colors in stock are offered; the uncolored ribbon asks nothing.
    expect(within(swatches).getAllByRole("radio").map((r) => r.getAttribute("aria-label"))).toEqual(["Đỏ", "Vàng"]);
    expect(within(dialog).queryByRole("radiogroup", { name: "Màu – Nơ đỏ" })).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Tạo lệnh" }));
    expect(await screen.findByText(KIT_MESSAGES.colorPick(["Bánh quy bơ"]), { exact: false })).toBeInTheDocument();
    expect(created).toEqual([]);

    await user.click(within(swatches).getByRole("radio", { name: "Vàng" }));
    await waitFor(() => expect(previews).toContainEqual({ kit_id: "kit-1", planned_qty: "20", colors: [{ sku_id: "sku-1", color_id: "c-vang" }] }));
    await user.click(within(dialog).getByRole("button", { name: "Tạo lệnh" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(created).toEqual([{ kit_id: "kit-1", planned_qty: "20", colors: [{ sku_id: "sku-1", color_id: "c-vang" }] }]);
  });

  it("plans without colors when the recipe fixes the product's color", async () => {
    const { created } = planServer("c-do");
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyDialog kitId="kit-1" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
    await within(dialog).findByRole("option", { name: "Giỏ An Khang v1" });
    // Until the recipe and its SKUs have loaded the dialog asks to wait; click again until it plans.
    await waitFor(async () => {
      if (created.length === 0) await user.click(within(dialog).getByRole("button", { name: "Tạo lệnh" }));
      expect(created).toHaveLength(1);
    });
    expect(created).toEqual([{ kit_id: "kit-1", planned_qty: "20" }]);
    expect(within(dialog).queryByRole("radiogroup")).not.toBeInTheDocument();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("asks before closing while a create may have been stored without an answer", async () => {
    const { created } = planServer("c-do", 1);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyDialog kitId="kit-1" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
    await within(dialog).findByRole("option", { name: "Giỏ An Khang v1" });
    await waitFor(async () => {
      if (created.length === 0) await user.click(within(dialog).getByRole("button", { name: "Tạo lệnh" }));
      expect(created).toHaveLength(1);
    });
    expect(await screen.findByText(OUTCOME_UNKNOWN_MESSAGE)).toBeInTheDocument();

    await user.click(await within(dialog).findByRole("button", { name: "Hủy" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(CLOSE_UNSETTLED_MESSAGE);
    expect(within(dialog).getByRole("button", { name: "Ở lại" })).toHaveFocus();
    await user.click(within(dialog).getByRole("button", { name: "Ở lại" }));
    expect(within(dialog).queryByText(CLOSE_UNSETTLED_MESSAGE)).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Đóng" }));
    await user.keyboard("{Escape}");
    expect(within(dialog).queryByText(CLOSE_UNSETTLED_MESSAGE)).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Đóng" }));
    await user.click(within(dialog).getByRole("button", { name: "Vẫn đóng" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes without asking when nothing may have been stored", async () => {
    planServer("c-do");
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyDialog kitId="kit-1" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("resends a create that got no answer under the same key, and says edits made since were not saved", async () => {
    const { created, keys } = planServer("c-do", 1);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyDialog kitId="kit-1" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
    await within(dialog).findByRole("option", { name: "Giỏ An Khang v1" });
    await waitFor(async () => {
      if (created.length === 0) await user.click(within(dialog).getByRole("button", { name: "Tạo lệnh" }));
      expect(created).toHaveLength(1);
    });
    expect(await screen.findByText(OUTCOME_UNKNOWN_MESSAGE)).toBeInTheDocument();

    const planned = within(dialog).getByRole("textbox", { name: "Số giỏ kế hoạch" });
    await user.clear(planned);
    await user.type(planned, "25");
    await user.click(within(dialog).getByRole("button", { name: "Tạo lệnh" }));

    expect(await screen.findByText(KIT_MESSAGES.assemblyCreatedAsSent(testAssemblyDetail().doc_no))).toBeInTheDocument();
    expect(created).toEqual([{ kit_id: "kit-1", planned_qty: "20" }, { kit_id: "kit-1", planned_qty: "20" }]);
    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBe(keys[0]);
    expect(onClose).not.toHaveBeenCalled();
  });
});
