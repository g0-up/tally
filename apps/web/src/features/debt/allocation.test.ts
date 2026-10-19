import { describe, expect, it } from "vitest";
import type { Money } from "../../shared/domain/money";
import { byOldestDue, checkAllocation, suggestAllocation, type OpenDocument } from "./allocation";

const doc = (id: string, open: string, next_due: string, business_date = "2026-09-01"): OpenDocument => ({ id, open: open as Money, next_due, business_date, source_doc_no: id });
const m = (v: string) => v as Money;

describe("suggestAllocation", () => {
  const docs = [doc("SO-0003", "700000.5", "2026-10-01"), doc("SO-0001", "1000000.25", "2026-09-05"), doc("SO-0002", "0", "2026-09-01"), doc("SO-0004", "300000", "2026-09-05", "2026-08-01")];

  it("fills the oldest due first, ties by document date, and skips settled documents", () => {
    expect(byOldestDue(docs).map((d) => d.id)).toEqual(["SO-0004", "SO-0001", "SO-0003"]);
    const split = suggestAllocation(m("1500000.30"), docs);
    expect(Object.fromEntries(split)).toEqual({ "SO-0004": "300000", "SO-0001": "1000000.25", "SO-0003": "200000.05" });
  });

  it("stops at what is open and leaves the rest as an advance", () => {
    const split = suggestAllocation(m("5000000"), docs);
    expect(Object.fromEntries(split)).toEqual({ "SO-0004": "300000", "SO-0001": "1000000.25", "SO-0003": "700000.5" });
    const check = checkAllocation(m("5000000"), [...split].map(([id, amount]) => ({ id, open: docs.find((d) => d.id === id)?.open ?? m("0"), amount })));
    expect(check.allocated.toString()).toBe("2000000.75");
    expect(check.advance.toString()).toBe("2999999.25");
    expect(check.overPayment).toBe(false);
  });

  it("allocates nothing without an amount", () => {
    expect([...suggestAllocation(null, docs).values()]).toEqual(["0", "0", "0"]);
  });
});

describe("checkAllocation", () => {
  it("flags lines over the payment and over their open amount, without float drift", () => {
    const lines = [
      { id: "a", open: m("0.3"), amount: m("0.1") },
      { id: "b", open: m("0.2"), amount: m("0.2") },
    ];
    const exact = checkAllocation(m("0.3"), lines);
    expect(exact.overPayment).toBe(false);
    expect(exact.advance.toString()).toBe("0");

    const over = checkAllocation(m("0.25"), [...lines, { id: "c", open: m("100"), amount: m("100.0001") }]);
    expect(over.overPayment).toBe(true);
    expect(over.advance.toString()).toBe("0");
    expect(over.overOpen).toEqual(["c"]);
  });
});
