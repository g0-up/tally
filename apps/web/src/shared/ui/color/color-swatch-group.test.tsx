import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { ColorCode } from "../../domain/color";
import { ColorSwatchGroup } from "./color-swatch-group";

function Harness({ initial = null }: { initial?: ColorCode | null }) {
  const [value, setValue] = useState<ColorCode | null>(initial);
  return <ColorSwatchGroup value={value} onChange={setValue} />;
}

describe("ColorSwatchGroup", () => {
  it("offers the seven colors as radios and no text input", () => {
    render(<Harness />);
    expect(screen.getByRole("radiogroup", { name: "Màu sắc" })).toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(7);
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.getByText("Chưa chọn")).toBeInTheDocument();
  });

  it("picks a color on click and shows its name", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("radio", { name: "Tím" }));
    expect(screen.getByRole("radio", { name: "Tím" })).toBeChecked();
    expect(screen.getByText("Tím", { selector: ".sw-n" })).toBeInTheDocument();
  });

  it("moves the choice with the arrow keys and wraps around", async () => {
    const user = userEvent.setup();
    render(<Harness initial="den" />);
    await user.click(screen.getByRole("radio", { name: "Đen" }));
    // Radix checks the radio that receives focus while an arrow key is held down, as in a browser.
    const press = async (key: string, expected: string) => {
      await user.keyboard(`{${key}>}`);
      await waitFor(() => expect(screen.getByRole("radio", { name: expected })).toHaveFocus());
      await user.keyboard(`{/${key}}`);
      expect(screen.getByRole("radio", { name: expected })).toBeChecked();
    };
    await press("ArrowRight", "Trắng");
    await press("ArrowRight", "Xanh");
    await press("ArrowLeft", "Trắng");
  });
});
