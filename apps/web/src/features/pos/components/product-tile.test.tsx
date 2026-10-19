import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { posTileSchema } from "../../sales/schemas";
import { ProductTile } from "./product-tile";

const TILE = posTileSchema.parse({
  sku_id: "sku-keo",
  sku_code: "KEO-DUA",
  name: "Kẹo dừa",
  variant_label: "",
  unit_id: "unit-goi",
  unit_name: "Gói",
  retail_price: "50000",
  stock: [{ color_id: null, qty: "40" }],
});

describe("ProductTile", () => {
  it("includes the colors still on sale in the button name and hides the dots", () => {
    const { container } = render(<ProductTile tile={TILE} price={toMoney("50000")} available="40" colors={["do", "vang"]} onAdd={vi.fn()} />);
    const tile = screen.getByRole("button", { name: /^Kẹo dừa/ });
    expect(tile).toHaveAccessibleName(/Có thể bán: 40 · Màu còn bán: Đỏ, Vàng$/);
    expect(container.querySelector(".tdots")).toHaveAttribute("aria-hidden", "true");
    expect(container.querySelector(".tdots")).not.toHaveAttribute("aria-label");
  });

  it("names no colors for a product sold without them", () => {
    render(<ProductTile tile={TILE} price={toMoney("50000")} available="40" colors={[]} onAdd={vi.fn()} />);
    expect(screen.getByRole("button", { name: /^Kẹo dừa/ })).not.toHaveAccessibleName(/Màu còn bán/);
  });
});
