import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { UNIT_THUNG, testSku } from "../testing/fixtures";

/** Bánh quy bơ sold by the thùng of 12 at its own 170.000. */
const SKU = testSku({
  units: testSku().units.map((u) => (u.unit_id === UNIT_THUNG ? { ...u, retail_price: toMoney(170000) } : u)),
});

describe("Danh sách hàng hóa page", () => {
  it("clears a unit's own price from the sell-price dialog, so it sells at base price × factor", async () => {
    const bodies: unknown[] = [];
    server.use(
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [SKU], next_cursor: null })),
      http.get(apiUrl("/categories"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/price-lists"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/price-changes"), () => HttpResponse.json({ data: [], next_cursor: null })),
      http.get(apiUrl("/skus/sku-1/price-history"), () => HttpResponse.json({ data: [], next_cursor: null })),
      http.post(apiUrl("/skus/sku-1/sale-price"), async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ data: [] });
      }),
    );
    const user = userEvent.setup();
    renderApp("/purchasing/products");
    const table = await screen.findByRole("table", { name: "Danh sách hàng hóa" });
    await user.click(within(table).getByRole("button", { name: "Sửa giá bán" }));

    const dialog = await screen.findByRole("dialog", { name: "Sửa giá bán · Bánh quy bơ" });
    expect(within(dialog).getByLabelText("Giá bán lẻ / hộp")).toHaveValue("15.000");
    const carton = within(dialog).getByLabelText("Giá bán lẻ / thùng");
    expect(carton).toHaveValue("170.000");
    expect(within(dialog).getByLabelText("Giá sỉ / thùng")).toHaveAttribute("placeholder", "0 tự tính");

    await user.clear(carton);
    expect(carton).toHaveAttribute("placeholder", "180.000 tự tính");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Sửa giá bán · Bánh quy bơ" })).not.toBeInTheDocument());
    expect(bodies).toEqual([{ kind: "retail", price: null, unit_id: UNIT_THUNG, source: "product_list" }]);
  });
});
