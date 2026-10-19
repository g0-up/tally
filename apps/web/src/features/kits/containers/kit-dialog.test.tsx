import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { UNIT_HOP } from "../../procurement/testing/fixtures";
import { KIT_MESSAGES } from "../api/adapters";
import type { CostView } from "../schemas";
import { KIT_UNITS, UNIT_GIO, UNIT_GOI, testCostView, testKit, testMixGroup } from "../testing/fixtures";
import { KitDialog } from "./kit-dialog";

interface Calls {
  previews: unknown[];
  created: unknown[];
  skuPatches: { body: unknown; ifMatch: string | null }[];
  published: number;
}

/** Lookups of the editor, a preview answering `cost`, and recorders for every write. */
function kitServer(cost: CostView): Calls {
  const calls: Calls = { previews: [], created: [], skuPatches: [], published: 0 };
  server.use(
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: KIT_UNITS, next_cursor: null })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.get(apiUrl("/mix-groups"), () => HttpResponse.json({ data: [testMixGroup(), testMixGroup({ id: "mix-off", status: "inactive" })], next_cursor: null })),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.post(/\/kits:preview$/, async ({ request }) => {
      calls.previews.push(await request.json());
      return HttpResponse.json(cost);
    }),
    http.post(apiUrl("/kits"), async ({ request }) => {
      calls.created.push(await request.json());
      return HttpResponse.json(testKit(), { status: 201 });
    }),
    http.get(apiUrl("/skus/sku-kit"), () => HttpResponse.json({ version: 4 })),
    http.patch(apiUrl("/skus/sku-kit"), async ({ request }) => {
      calls.skuPatches.push({ body: await request.json(), ifMatch: request.headers.get("If-Match") });
      return HttpResponse.json({});
    }),
    http.post(/\/kits\/kit-1:publish$/, () => {
      calls.published += 1;
      return HttpResponse.json(testKit({ status: "active" }));
    }),
  );
  return calls;
}

/** Fills the header and replaces the empty component line with a mix of 2 gói Dâu and `cam` gói Cam. */
async function fillMixKit(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, cam: string) {
  await user.type(within(dialog).getByRole("textbox", { name: "Mã" }), "GQAK");
  await user.type(within(dialog).getByRole("textbox", { name: "Tên" }), "Giỏ An Khang");
  await user.type(within(dialog).getByRole("textbox", { name: "Giá bán lẻ" }), "500000");
  await user.click(within(dialog).getByRole("button", { name: "Xóa thành phần 1" }));
  await user.click(await within(dialog).findByRole("button", { name: "+ Thêm thùng mix vị" }));
  await user.type(await within(dialog).findByRole("textbox", { name: /^Dâu · còn .+ gói$/ }), "2");
  await user.type(within(dialog).getByRole("textbox", { name: /^Cam · còn .+ gói$/ }), cam);
}

describe("KitDialog", () => {
  it("warns about a kit priced above its parts with a thin margin, and saves a mix pack as a draft", async () => {
    const calls = kitServer(testCostView());
    const onClose = vi.fn();
    const onPreview = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={onClose} onPreview={onPreview} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");

    expect(await within(dialog).findByText("Giá giỏ cao hơn tổng giá bán lẻ các thành phần.")).toBeInTheDocument();
    expect(within(dialog).getByText("Biên lãi gộp dự kiến dưới 15%.")).toBeInTheDocument();
    const mixItem = { kind: "mix_pack", mix_group_id: "mix-1", unit_id: UNIT_GOI, qty: "3", flavors: [{ sku_id: "sku-dau", parts: "2" }, { sku_id: "sku-cam", parts: "1" }] };
    expect(calls.previews.at(-1)).toEqual({ items: [mixItem], packing_cost: "20000", kit_price: "500000" });

    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));

    await waitFor(() => expect(onPreview).toHaveBeenCalledWith("kit-1"));
    expect(onClose).toHaveBeenCalled();
    expect(calls.created).toEqual([
      { type: "kit", name: "Giỏ An Khang", sku_code: "GQAK", base_unit_id: UNIT_GIO, retail_price: "500000", min_stock: "10", recipe: { items: [mixItem], packing_cost: "20000" } },
    ]);
    // The create request carries the minimum stock, so nothing is written to the SKU after it.
    expect(calls.skuPatches).toEqual([]);
    expect(calls.published).toBe(0);
    expect(await screen.findByText(/Đã lưu nháp/)).toBeInTheDocument();
  });

  it("shows no warning for a healthy kit and refuses a mix pack with more flavors than it holds", async () => {
    const calls = kitServer(testCostView({ components_sale: "600000", total_cost: "300000" }));
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={vi.fn()} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");
    await waitFor(() => expect(calls.previews).not.toHaveLength(0));
    expect(await within(dialog).findByText("40%", { exact: false })).toBeInTheDocument();
    expect(within(dialog).queryByText("Giá giỏ cao hơn tổng giá bán lẻ các thành phần.")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Biên lãi gộp dự kiến dưới 15%.")).not.toBeInTheDocument();

    await user.type(within(dialog).getByRole("textbox", { name: /^Cam · còn .+ gói$/ }), "5");
    await user.click(within(dialog).getByRole("button", { name: "Lưu & phát hành" }));

    expect(await screen.findByText("Mix vị Kẹo mix vị: đã chọn 17 gói, vượt 3 gói.")).toBeInTheDocument();
    expect(calls.created).toEqual([]);
    expect(calls.published).toBe(0);
  });

  it("resends a create that got no answer under the same key, then sends the name edited since", async () => {
    kitServer(testCostView());
    const creates: { key: string | null; body: unknown }[] = [];
    const patched: unknown[] = [];
    server.use(
      http.post(apiUrl("/kits"), async ({ request }) => {
        creates.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        return creates.length === 1 ? HttpResponse.error() : HttpResponse.json(testKit(), { status: 201 });
      }),
      http.patch(apiUrl("/kits/kit-1"), async ({ request }) => {
        const body = (await request.json()) as { name: string };
        patched.push(body);
        return HttpResponse.json(testKit({ name: body.name, version: 2 }));
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={onClose} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    await waitFor(() => expect(creates).toHaveLength(1));
    await user.type(within(dialog).getByRole("textbox", { name: "Tên" }), " Tết");
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const [first, retry] = creates;
    expect(first?.key).toBeTruthy();
    // The same request again, so the server answers the kit it may have stored instead of a second one.
    expect(retry).toEqual(first);
    expect(patched).toEqual([expect.objectContaining({ name: "Giỏ An Khang Tết" })]);
  });

  it("says a kit created by an unanswered attempt keeps that attempt's code", async () => {
    kitServer(testCostView());
    let creates = 0;
    server.use(
      http.post(apiUrl("/kits"), () => {
        creates += 1;
        return creates === 1 ? HttpResponse.error() : HttpResponse.json(testKit(), { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={onClose} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    await waitFor(() => expect(creates).toBe(1));
    await user.type(within(dialog).getByRole("textbox", { name: "Mã" }), "2");
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));

    expect(await screen.findByText(KIT_MESSAGES.createdAsSent("Giỏ An Khang", "GQAK"))).toBeInTheDocument();
    // None of the steps after the create ran: the notice names what the form holds that is not stored.
    expect(screen.getByText(/Tên, thành phần, giá và tồn tối thiểu vừa sửa chưa được lưu, giỏ chưa phát hành\./)).toBeInTheDocument();
    expect(creates).toBe(2);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("creates the kit once when a later step fails, and sends the edits made before the retry", async () => {
    const calls = kitServer(testCostView());
    const patched: { body: unknown; ifMatch: string | null }[] = [];
    const published: (string | null)[] = [];
    server.use(
      http.patch(apiUrl("/kits/kit-1"), async ({ request }) => {
        const body = (await request.json()) as { name: string };
        patched.push({ body, ifMatch: request.headers.get("If-Match") });
        return HttpResponse.json(testKit({ name: body.name, version: 2 }));
      }),
      http.post(/\/kits\/kit-1:publish$/, ({ request }) => {
        published.push(request.headers.get("If-Match"));
        return published.length === 1 ? HttpResponse.error() : HttpResponse.json(testKit({ name: "Giỏ An Khang Tết", status: "active", version: 3 }));
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={onClose} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");
    await user.click(within(dialog).getByRole("button", { name: "Lưu & phát hành" }));

    expect(await screen.findByText(/Đã lưu nháp Giỏ An Khang nhưng chưa phát hành/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    // The kit exists now: its code and type are fixed, the rest can still change.
    expect(within(dialog).getByRole("textbox", { name: "Mã" })).toBeDisabled();
    await user.type(within(dialog).getByRole("textbox", { name: "Tên" }), " Tết");
    const minStock = within(dialog).getByRole("textbox", { name: "Tồn tối thiểu (giỏ)" });
    await user.clear(minStock);
    await user.type(minStock, "12");
    await user.click(within(dialog).getByRole("button", { name: "Lưu & phát hành" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls.created).toHaveLength(1);
    expect(patched).toEqual([{ body: expect.objectContaining({ name: "Giỏ An Khang Tết" }), ifMatch: etagOf(1) }]);
    // Only the minimum stock changed after the create is written to the SKU.
    expect(calls.skuPatches).toEqual([{ body: { min_stock: "12" }, ifMatch: etagOf(4) }]);
    expect(published).toEqual([etagOf(1), etagOf(2)]);
  });

  it("sends a combo's wholesale price in the create request and no minimum stock", async () => {
    const calls = kitServer(testCostView());
    server.use(
      http.post(apiUrl("/kits"), async ({ request }) => {
        calls.created.push(await request.json());
        return HttpResponse.json(testKit({ type: "combo", base_unit_id: UNIT_HOP }), { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog type="combo" onClose={onClose} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    expect(within(dialog).queryByRole("textbox", { name: "Tồn tối thiểu (giỏ)" })).not.toBeInTheDocument();
    await fillMixKit(user, dialog, "1");
    await user.type(within(dialog).getByRole("textbox", { name: "Giá sỉ / doanh nghiệp" }), "450000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls.created).toEqual([expect.objectContaining({ type: "combo", retail_price: "500000", wholesale_price: "450000" })]);
    expect(calls.created[0]).not.toHaveProperty("min_stock");
    expect(calls.skuPatches).toEqual([]);
  });

  it.each([
    ["retail_price", "must_not_be_negative", "Giá bán lẻ", KIT_MESSAGES.retailNegative],
    ["wholesale_price", "must_not_be_negative", "Giá sỉ / doanh nghiệp", KIT_MESSAGES.wholesaleNegative],
    ["min_stock", "must_not_be_negative", "Tồn tối thiểu (giỏ)", KIT_MESSAGES.minStockNegative],
    ["min_stock", "not_allowed", "Tồn tối thiểu (giỏ)", KIT_MESSAGES.minStockOnCombo],
  ])("words the API's refusal of %s as %s under its field", async (field, reason, label, message) => {
    const calls = kitServer(testCostView());
    server.use(
      http.post(apiUrl("/kits"), async ({ request }) => {
        calls.created.push(await request.json());
        return apiError(422, "validation_failed", { fields: { [field]: reason } });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={onClose} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");
    await user.type(within(dialog).getByRole("textbox", { name: "Giá sỉ / doanh nghiệp" }), "450000");
    const input = within(dialog).getByRole("textbox", { name: label });
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));

    expect(await within(dialog).findByText(message)).toBeInTheDocument();
    expect(input).toHaveAccessibleDescription(expect.stringContaining(message));
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(calls.created).toHaveLength(1);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("resends a publish that got no answer under its own key of the intent, so the kit is published once", async () => {
    const calls = kitServer(testCostView());
    const creates: (string | null)[] = [];
    const publishes: { key: string | null; ifMatch: string | null }[] = [];
    server.use(
      http.post(apiUrl("/kits"), async ({ request }) => {
        creates.push(request.headers.get("Idempotency-Key"));
        calls.created.push(await request.json());
        return HttpResponse.json(testKit(), { status: 201 });
      }),
      http.post(/\/kits\/kit-1:publish$/, ({ request }) => {
        publishes.push({ key: request.headers.get("Idempotency-Key"), ifMatch: request.headers.get("If-Match") });
        return publishes.length === 1 ? HttpResponse.error() : HttpResponse.json(testKit({ status: "active", version: 2 }));
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={onClose} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await fillMixKit(user, dialog, "1");
    await user.click(within(dialog).getByRole("button", { name: "Lưu & phát hành" }));
    expect(await screen.findByText(/Đã lưu nháp Giỏ An Khang nhưng chưa phát hành/)).toBeInTheDocument();
    await user.click(await within(dialog).findByRole("button", { name: "Lưu & phát hành" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls.created).toHaveLength(1);
    expect(publishes).toHaveLength(2);
    const [first, retry] = publishes;
    expect(first?.key).toBe(`${creates[0]}:publish`);
    expect(retry).toEqual(first);
  });

  it("names every recipe line's controls by its position and announces a mix line going over its room", async () => {
    kitServer(testCostView());
    const user = userEvent.setup();
    renderWithProviders(<KitDialog onClose={vi.fn()} onPreview={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Tạo giỏ quà / combo" });
    await user.click(within(dialog).getByRole("button", { name: "+ Thêm thành phần" }));
    await user.click(await within(dialog).findByRole("button", { name: "+ Thêm thùng mix vị" }));

    for (const n of [1, 2]) {
      expect(within(dialog).getByRole("combobox", { name: `Thành phần ${n}` })).toBeInTheDocument();
      expect(within(dialog).getByRole("textbox", { name: `Số lượng thành phần ${n}` })).toBeInTheDocument();
      expect(within(dialog).getByRole("button", { name: `Xóa thành phần ${n}` })).toBeInTheDocument();
    }
    expect(within(dialog).getByRole("combobox", { name: "Chọn nhóm mix vị thành phần 3" })).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Số lượng mix thành phần 3" })).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Đơn vị mix thành phần 3" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Xóa thành phần 3" })).toBeInTheDocument();

    const flavors = within(dialog).getByRole("group", { name: "Số gói theo vị thành phần 3" });
    const dau = within(flavors).getByRole("textbox", { name: /^Dâu · còn .+ gói$/ });
    expect(within(flavors).getByRole("textbox", { name: /^Cam · còn .+ gói$/ })).toBeInTheDocument();

    const status = flavors.nextElementSibling as HTMLElement;
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveTextContent("Đã chọn 0/3 gói");
    await user.type(dau, "5");
    expect(flavors.nextElementSibling).toBe(status);
    expect(status).toHaveTextContent("Đã chọn 5 gói, vượt 3 gói. Giảm bớt 2 gói.");
    expect(within(status).queryByRole("alert")).not.toBeInTheDocument();
    expect(within(status).queryByRole("status")).not.toBeInTheDocument();
  });
});
