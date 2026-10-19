import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { recipeEditorHandlers, testKit } from "../testing/fixtures";
import { ComboEditDialog } from "./combo-edit-dialog";

const combo = testKit({ product_id: "kit-3", type: "combo", name: "Combo Tết", status: "active", active_version: 1, sku_id: "sku-combo", sku_code: "CBT" });
const skus = [testSku(), testSku({ id: "sku-combo", code: "CBT", display_name: "Combo Tết", retail_price: toMoney(40000), wholesale_price: toMoney(38000) })];

describe("ComboEditDialog", () => {
  it("saves the new components, publishes them and then the changed price", async () => {
    const calls: string[] = [];
    const bodies: unknown[] = [];
    server.use(
      ...recipeEditorHandlers(skus),
      http.post(/\/kits\/kit-3\/recipes$/, async ({ request }) => {
        calls.push("recipe");
        bodies.push(await request.json());
        return HttpResponse.json({ ...combo, version: 2, latest: { ...combo.latest, version_no: 2, status: "draft" } }, { status: 201 });
      }),
      http.post(/\/kits\/kit-3:publish$/, () => {
        calls.push("publish");
        return HttpResponse.json({ ...combo, version: 3 });
      }),
      http.post(/\/skus\/sku-combo\/sale-price$/, async ({ request }) => {
        calls.push("price");
        bodies.push(await request.json());
        return HttpResponse.json({});
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ComboEditDialog kit={combo} onClose={onClose} />);

    // The form mounts once the SKUs it edits are loaded; until then a pending dialog shares the title.
    const price = await screen.findByRole("textbox", { name: "Giá bán lẻ" });
    const dialog = screen.getByRole("dialog", { name: "Sửa combo Combo Tết" });
    await user.clear(price);
    await user.type(price, "36000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    // Below the current wholesale price, so the retail price goes after the new version publishes.
    expect(calls).toEqual(["recipe", "publish", "price"]);
    expect(bodies).toEqual([{ items: [{ kind: "goods", sku_id: "sku-1", qty: "3" }] }, { kind: "retail", price: "36000", source: "catalog" }]);
    expect(await screen.findByText("Đã cập nhật combo.")).toBeInTheDocument();
  });

  it("resends a price write that got no answer under the same key, after the steps that went through", async () => {
    const calls: string[] = [];
    const keys: Array<string | null> = [];
    server.use(
      ...recipeEditorHandlers(skus),
      http.post(/\/kits\/kit-3\/recipes$/, () => {
        calls.push("recipe");
        return HttpResponse.json({ ...combo, version: 2, latest: { ...combo.latest, version_no: 2, status: "draft" } }, { status: 201 });
      }),
      http.post(/\/kits\/kit-3:publish$/, () => {
        calls.push("publish");
        return HttpResponse.json({ ...combo, version: 3 });
      }),
      http.post(/\/skus\/sku-combo\/sale-price$/, ({ request }) => {
        calls.push("price");
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json({});
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ComboEditDialog kit={combo} onClose={onClose} />);

    const price = await screen.findByRole("textbox", { name: "Giá bán lẻ" });
    const dialog = screen.getByRole("dialog", { name: "Sửa combo Combo Tết" });
    await user.clear(price);
    await user.type(price, "36000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu" }));
    await waitFor(() => expect(keys).toHaveLength(1));
    expect(onClose).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Lưu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls).toEqual(["recipe", "publish", "price", "price"]);
    expect(keys[0]).toMatch(/:sale-price:sku-combo:retail:general:catalog:36000:1$/);
    expect(keys[1]).toBe(keys[0]);
  });
});
