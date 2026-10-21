import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { TEST_UNITS, testSku } from "../../procurement/testing/fixtures";
import type { SkuDetail } from "../schemas";

const GOODS = testSku();
const COMBO = testSku({ id: "sku-combo", code: "CB01", display_name: "Combo quà Tết", product_type: "combo" });

/** `GET /skus/{id}` of a listed SKU: its base unit row carries the base prices. */
const detailOf = (sku: typeof GOODS): SkuDetail => ({
  id: sku.id,
  display_name: sku.display_name,
  base_unit_id: sku.base_unit_id,
  version: 2,
  units: sku.units.map((u) => (u.is_base ? { ...u, retail_price: sku.retail_price, wholesale_price: sku.wholesale_price } : u)),
  barcodes: [],
});

describe("ProductsPage", () => {
  it("opens the unit and price editor from a goods row only", async () => {
    server.use(
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [GOODS, COMBO], next_cursor: null })),
      http.get(apiUrl("/skus/sku-1"), () => HttpResponse.json(detailOf(GOODS))),
      http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS, next_cursor: null })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    );
    renderApp("/catalog/products");
    const table = await screen.findByRole("table", { name: "Hàng hóa" });

    const combo = within(table).getByRole("row", { name: /Combo quà Tết/ });
    expect(within(combo).queryByRole("button", { name: "Đơn vị & giá Combo quà Tết" })).not.toBeInTheDocument();
    expect(within(combo).getByRole("button", { name: "Mã vạch Combo quà Tết" })).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(within(table).getByRole("button", { name: "Đơn vị & giá Bánh quy bơ" }));

    await screen.findByRole("group", { name: "Đơn vị tính & giá bán" });
    const dialog = screen.getByRole("dialog", { name: "Đơn vị & giá · Bánh quy bơ" });
    expect(within(dialog).getByLabelText("Giá bán lẻ / hộp")).toHaveValue("15.000");
    expect(within(dialog).getByLabelText("Quy đổi: 1 thùng bằng bao nhiêu hộp")).toHaveValue("12");
  });
});
