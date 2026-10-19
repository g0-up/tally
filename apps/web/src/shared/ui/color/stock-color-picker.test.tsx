import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ColorKey } from "../../domain/color";
import { StockColorPicker, type StockColorOption } from "./stock-color-picker";

const OPTIONS: readonly StockColorOption[] = [
  { color: "do", qty: "4" },
  { color: "xanh", qty: "0" },
  { color: "vang", qty: "2" },
];

function Picker({ label, initial = null }: { label?: string; initial?: ColorKey | null }) {
  const [value, setValue] = useState<ColorKey | null>(initial);
  return <StockColorPicker options={OPTIONS} value={value} onChange={setValue} base="hộp" {...(label ? { label } : {})} />;
}

describe("StockColorPicker", () => {
  it("is a radio group named for its row, described by what is still needed", () => {
    render(<Picker label="Màu khách lấy – Mứt gừng" />);
    const group = screen.getByRole("radiogroup", { name: "Màu khách lấy – Mứt gừng" });
    expect(group).toHaveAccessibleDescription("Chọn màu");
    expect(screen.getByRole("radio", { name: "Đỏ 4" })).not.toBeChecked();
    expect(screen.getByRole("radio", { name: "Xanh 0" })).toBeDisabled();
  });

  it("moves between colors with the arrow keys, skipping a sold-out one, and picks with Space", async () => {
    const user = userEvent.setup();
    render(<Picker />);
    await user.tab();
    expect(screen.getByRole("radio", { name: "Đỏ 4" })).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("radio", { name: "Vàng 2" })).toHaveFocus();
    await user.keyboard(" ");
    expect(screen.getByRole("radio", { name: "Vàng 2" })).toBeChecked();
    expect(screen.getByRole("radiogroup", { name: "Màu khách lấy" })).not.toHaveAttribute("aria-describedby");
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("radio", { name: "Đỏ 4" })).toHaveFocus();
  });

  it("says a chosen color has sold out", () => {
    render(<StockColorPicker options={OPTIONS} value="xanh" onChange={vi.fn()} base="hộp" />);
    expect(screen.getByRole("radiogroup", { name: "Màu khách lấy" })).toHaveAccessibleDescription("Màu này đã hết");
    expect(screen.getByRole("radio", { name: "Xanh 0" })).toBeEnabled();
  });
});
