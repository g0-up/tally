import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { HotkeysProvider } from "../../app/providers/hotkeys-provider";
import { DatePicker } from "./date-picker";
import { Field } from "./field";

function Due({ initial = null, error }: { initial?: string | null; error?: string }) {
  const [value, setValue] = useState<string | null>(initial);
  return (
    <HotkeysProvider>
      <Field label="Hạn trả" error={error}>
        <DatePicker value={value} onChange={setValue} today="2026-10-15" />
      </Field>
      <output aria-label="Giá trị">{value ?? "trống"}</output>
    </HotkeysProvider>
  );
}

const valueShown = () => screen.getByRole("status", { name: "Giá trị" });

describe("DatePicker", () => {
  it("takes a typed dd/mm/yyyy date and shows it back in that format", async () => {
    const user = userEvent.setup();
    render(<Due />);
    const input = screen.getByRole("textbox", { name: "Hạn trả" });
    await user.type(input, "03/11/2026");
    expect(valueShown()).toHaveTextContent("2026-11-03");
    await user.clear(input);
    expect(valueShown()).toHaveTextContent("trống");
  });

  it("is named by its field and described by the field's error", () => {
    render(<Due error="Hạn trả phải sau ngày bán." />);
    const input = screen.getByRole("textbox", { name: "Hạn trả" });
    expect(input).toHaveAccessibleDescription("Hạn trả phải sau ngày bán.");
    expect(input).toHaveAttribute("aria-invalid", "true");
  });

  it("walks the calendar by day, week, week edge and month from the keyboard and picks with Enter", async () => {
    const user = userEvent.setup();
    render(<Due initial="2026-10-15" />);
    await user.click(screen.getByRole("button", { name: "Chọn ngày trên lịch" }));
    const calendar = await screen.findByRole("dialog", { name: "Lịch chọn ngày" });
    const day = (label: string) => within(calendar).getByRole("button", { name: label });
    await waitFor(() => expect(day("Thứ năm, 15/10/2026")).toHaveFocus());

    await user.keyboard("{ArrowRight}");
    expect(day("Thứ sáu, 16/10/2026")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(day("Thứ sáu, 23/10/2026")).toHaveFocus();
    await user.keyboard("{Home}");
    expect(day("Thứ hai, 19/10/2026")).toHaveFocus();
    await user.keyboard("{End}");
    expect(day("Chủ nhật, 25/10/2026")).toHaveFocus();
    await user.keyboard("{ArrowUp}{ArrowLeft}");
    expect(day("Thứ bảy, 17/10/2026")).toHaveFocus();
    await user.keyboard("{PageDown}");
    expect(within(calendar).getByText("Tháng 11/2026")).toBeInTheDocument();
    // A month step lands on the first day of that month.
    expect(day("Chủ nhật, 01/11/2026")).toHaveFocus();

    await user.keyboard("{Enter}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Lịch chọn ngày" })).not.toBeInTheDocument());
    expect(valueShown()).toHaveTextContent("2026-11-01");
    expect(screen.getByRole("textbox", { name: "Hạn trả" })).toHaveValue("01/11/2026");
  });

  it("closes the calendar on Esc without picking and hands focus back to its button", async () => {
    const user = userEvent.setup();
    render(<Due initial="2026-10-15" />);
    const button = screen.getByRole("button", { name: "Chọn ngày trên lịch" });
    await user.click(button);
    const calendar = await screen.findByRole("dialog", { name: "Lịch chọn ngày" });
    await waitFor(() => expect(within(calendar).getByRole("button", { name: "Thứ năm, 15/10/2026" })).toHaveFocus());
    await user.keyboard("{ArrowRight}{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Lịch chọn ngày" })).not.toBeInTheDocument());
    expect(valueShown()).toHaveTextContent("2026-10-15");
    await waitFor(() => expect(button).toHaveFocus());
  });
});
