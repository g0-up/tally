import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Period } from "./period";
import { PeriodBar } from "./period-bar";

const TODAY = "2026-10-12";

function setup(over: { period?: Period; date?: string } = {}) {
  const props = {
    period: over.period ?? "month",
    date: over.date ?? TODAY,
    today: TODAY,
    onPeriodChange: vi.fn<(p: Period) => void>(),
    onDateChange: vi.fn<(d: string) => void>(),
    onPrev: vi.fn<() => void>(),
    onNext: vi.fn<() => void>(),
    onToday: vi.fn<() => void>(),
  };
  render(<PeriodBar {...props} />);
  return props;
}

describe("PeriodBar", () => {
  it("presses the chip of the current period and asks for another one", async () => {
    const props = setup({ period: "month" });
    const user = userEvent.setup();

    const chips = within(screen.getByRole("group", { name: "Chọn kỳ" })).getAllByRole("button");
    expect(chips.map((c) => c.textContent)).toEqual(["Ngày", "7 ngày", "Tháng", "Năm"]);
    expect(screen.getByRole("button", { name: "Tháng" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Năm" })).toHaveAttribute("aria-pressed", "false");

    await user.click(screen.getByRole("button", { name: "7 ngày" }));
    expect(props.onPeriodChange).toHaveBeenCalledWith("7d");
  });

  it.each([
    ["day", "Ngày"],
    ["7d", "Đến ngày"],
    ["month", "Trong tháng có ngày"],
    ["year", "Trong năm có ngày"],
  ] as const)("labels the anchor date of a %s period “%s”", (period, label) => {
    setup({ period });
    expect(screen.getByText(label, { selector: "label span" })).toBeInTheDocument();
  });

  it("steps to the previous and next period", async () => {
    const props = setup();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "‹ Kỳ trước" }));
    await user.click(screen.getByRole("button", { name: "Kỳ sau ›" }));
    expect(props.onPrev).toHaveBeenCalledTimes(1);
    expect(props.onNext).toHaveBeenCalledTimes(1);
  });

  it("offers the way back to today only away from the business date", () => {
    setup({ date: TODAY });
    expect(screen.queryByRole("button", { name: "Về hôm nay" })).not.toBeInTheDocument();
  });

  it("goes back to today from another date", async () => {
    const props = setup({ date: "2026-09-30" });
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Về hôm nay" }));
    expect(props.onToday).toHaveBeenCalledTimes(1);
  });

  it("moves the anchor to a typed date and ignores a cleared field", async () => {
    const props = setup({ date: "2026-09-30" });
    const user = userEvent.setup();
    const input = screen.getByRole("textbox");
    expect(input).toHaveValue("30/09/2026");

    await user.clear(input);
    expect(props.onDateChange).not.toHaveBeenCalled();
    await user.type(input, "05/10/2026");
    expect(props.onDateChange).toHaveBeenLastCalledWith("2026-10-05");
  });
});
