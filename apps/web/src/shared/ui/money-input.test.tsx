import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Money } from "../domain/money";
import { MoneyInput } from "./money-input";

/** A form that acts on the amount held by its parent when the button is pressed. */
function Form({ onPay }: { onPay: (amount: Money | null) => void }) {
  const [amount, setAmount] = useState<Money | null>(null);
  return (
    <>
      <MoneyInput aria-label="Khách đưa" value={amount} onChange={setAmount} />
      <button type="button" onClick={() => onPay(amount)}>
        Thanh toán
      </button>
    </>
  );
}

describe("MoneyInput", () => {
  it("hands each keystroke to its parent, so a click straight after typing acts on the typed amount", async () => {
    const onPay = vi.fn();
    const user = userEvent.setup();
    render(<Form onPay={onPay} />);

    await user.type(screen.getByRole("textbox", { name: "Khách đưa" }), "1.250.000");
    await user.click(screen.getByRole("button", { name: "Thanh toán" }));

    expect(onPay).toHaveBeenCalledExactlyOnceWith("1250000");
    expect(screen.getByRole("textbox", { name: "Khách đưa" })).toHaveValue("1.250.000");
  });
});
