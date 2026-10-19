import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Money } from "../../../shared/domain/money";
import { OFFLINE_BANK_TITLE, PaymentMethodPanel, type PaymentMethodPanelProps } from "./payment-method-panel";

const m = (s: string) => s as Money;

function renderPanel(props: Partial<PaymentMethodPanelProps> = {}) {
  const handlers = { onCash: vi.fn(), onBank: vi.fn(), onChip: vi.fn(), onCredit: vi.fn() };
  render(<PaymentMethodPanel total={m("185000")} cash={null} bank={null} credit={false} namedCustomer={false} {...handlers} {...props} />);
  return handlers;
}

describe("PaymentMethodPanel", () => {
  it("shows the change when the customer hands over more than the total", () => {
    renderPanel({ cash: m("200000") });
    expect(screen.getByRole("status")).toHaveTextContent("Trả lại khách: 15.000 ₫");
  });

  it("counts the transfer towards the change", () => {
    renderPanel({ cash: m("100000"), bank: m("100000") });
    expect(screen.getByRole("status")).toHaveTextContent("Trả lại khách: 15.000 ₫");
  });

  it("shows no change when the customer pays exactly", () => {
    renderPanel({ cash: m("185000") });
    expect(screen.queryByText(/Trả lại khách/)).not.toBeInTheDocument();
  });

  it("offers credit for the rest only to a named customer", async () => {
    const { onCredit } = renderPanel({ cash: m("100000"), namedCustomer: true });
    const box = screen.getByRole("checkbox", { name: /^Bán chịu phần còn lại 85\.000\s₫$/ });
    await userEvent.click(box);
    expect(onCredit).toHaveBeenCalledWith(true);
  });

  it("never offers credit to a walk-in buyer", () => {
    renderPanel({ cash: m("100000"), namedCustomer: false });
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("does not offer credit when nothing is owed", () => {
    renderPanel({ cash: m("185000"), namedCustomer: true });
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("puts a chip value through onChip", async () => {
    const { onChip } = renderPanel();
    const chips = screen.getByRole("group", { name: "Tiền khách đưa nhanh" });
    expect(chips.querySelectorAll("button")).toHaveLength(3);
    await userEvent.click(screen.getByRole("button", { name: /^200\.000\s₫$/ }));
    expect(onChip).toHaveBeenCalledWith("200000");
  });

  describe("offline", () => {
    it("disables the transfer and states the reason on screen and as its description", () => {
      renderPanel({ mode: "offline" });
      const bank = screen.getByRole("textbox", { name: "Chuyển khoản / QR" });
      expect(bank).toBeDisabled();
      expect(bank).toHaveAttribute("title", OFFLINE_BANK_TITLE);
      expect(bank).toHaveAccessibleDescription(OFFLINE_BANK_TITLE);
      expect(screen.getByText(OFFLINE_BANK_TITLE)).toBeVisible();
      expect(screen.getByRole("textbox", { name: "Tiền mặt khách đưa" })).toBeEnabled();
    });

    it("shows credit disabled with the offline suffix, even for a named customer", () => {
      renderPanel({ mode: "offline", cash: m("100000"), namedCustomer: true });
      const box = screen.getByRole("checkbox", { name: /^Bán chịu phần còn lại 85\.000\s₫ \(không cho phép khi ngoại tuyến\)$/ });
      expect(box).toBeDisabled();
      expect(box).not.toBeChecked();
    });

    it("counts only the cash towards the change", () => {
      renderPanel({ mode: "offline", cash: m("200000"), bank: m("100000") });
      expect(screen.getByRole("status")).toHaveTextContent("Trả lại khách: 15.000 ₫");
    });
  });
});
