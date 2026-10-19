import { describe, expect, it } from "vitest";
import type { Money } from "../../../shared/domain/money";
import { invoicePayment, paymentCodeOf } from "./adapters";

const m = (v: string) => v as Money;

describe("paymentCodeOf", () => {
  // The cases of the server's paymentStateOf, in its order of checks.
  it.each([
    ["0", "0", "paid"],
    ["0", "50000", "overpaid"],
    ["100000", "0", "unpaid"],
    ["100000", "40000", "partial"],
    ["100000", "100000.00", "paid"],
    ["100000", "120000", "overpaid"],
  ] as const)("reads a total of %s with %s paid as %s", (total, paid, code) => {
    expect(paymentCodeOf(total, paid)).toBe(code);
  });
});

describe("invoicePayment", () => {
  it("reads an invoice cancelled before any delivery, with nothing paid, as paid with nothing open", () => {
    const state = invoicePayment({ status: "cancelled_remaining", total_amount: m("250000"), delivered_amount: m("0"), paid_amount: m("0") });
    expect(state).toMatchObject({ total_amount: "0", paid_amount: "0", open_amount: "0", code: "paid", label: "Đã thanh toán đủ" });
  });
});
