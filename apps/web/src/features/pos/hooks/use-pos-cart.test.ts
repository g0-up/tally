import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Money } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import { getToasts } from "../../../shared/ui/toast-store";
import { cashChips } from "../api/adapters";
import { usePosCart, type PosProduct } from "./use-pos-cart";

const COOKIES: PosProduct = { skuId: "sku-1", skuLabel: "SP001 · Bánh quy bơ", unitId: "unit-hop", autoColor: null };
const CANDY: PosProduct = { skuId: "sku-2", skuLabel: "SP002 · Kẹo dừa", unitId: "unit-goi", autoColor: "do" };

describe("usePosCart", () => {
  it("counts a repeated product on its line and drops a line set to quantity 0", () => {
    const { result } = renderHook(() => usePosCart());
    act(() => {
      result.current.add(COOKIES);
      result.current.add(COOKIES);
      result.current.add(CANDY);
    });
    expect(result.current.state.lines).toHaveLength(2);
    expect(result.current.state.lines[0]?.qty).toBe("2");

    const key = result.current.state.lines[0]?.key ?? "";
    act(() => result.current.setQty(key, toQty(0)));
    expect(result.current.state.lines).toHaveLength(1);
    expect(result.current.state.lines[0]?.skuId).toBe("sku-2");
  });

  it("merges a line into the line of the color picked and says so in a toast", () => {
    const { result } = renderHook(() => usePosCart());
    act(() => result.current.add(CANDY));
    const first = result.current.state.lines[0]?.key ?? "";
    act(() => result.current.split(first));
    expect(result.current.state.lines).toHaveLength(2);

    const second = result.current.state.lines[1]?.key ?? "";
    act(() => result.current.pickColor(second, "do"));
    expect(result.current.state.lines).toHaveLength(1);
    expect(result.current.state.lines[0]).toMatchObject({ color: "do", qty: "2" });
    expect(getToasts().map((t) => t.message)).toContain("Đã gộp vào dòng Kẹo dừa Đỏ.");
  });

  it("allows credit only with a named customer and clears it when the buyer changes", () => {
    const { result } = renderHook(() => usePosCart());
    act(() => result.current.setCredit(true));
    expect(result.current.state.credit).toBe(false);

    act(() => result.current.setCustomer({ id: "c-1", name: "Tạp hóa Minh Anh" }));
    act(() => result.current.setCredit(true));
    expect(result.current.state.credit).toBe(true);

    act(() => result.current.setCustomer({ id: "c-2", name: "Cô Lan" }));
    expect(result.current.state.credit).toBe(false);
  });

  it("puts the chip value minus the transfer into the cash field", () => {
    const { result } = renderHook(() => usePosCart());
    act(() => result.current.setBank("200000" as Money));
    act(() => result.current.chip("500000" as Money));
    expect(result.current.state.cash).toBe("300000");
  });
});

describe("cashChips", () => {
  it.each([
    ["185000", ["185000", "200000", "500000"]],
    ["500000", ["500000"]],
    ["1230000", ["1230000", "1250000", "1300000", "500000"]],
    ["0", []],
  ])("total %s offers %j", (total, chips) => {
    expect(cashChips(total)).toEqual(chips);
  });

  it("never offers more than four chips or the same value twice", () => {
    for (const total of ["1", "49999", "50000", "99999", "123456", "499999", "987654"]) {
      const chips = cashChips(total);
      expect(chips.length).toBeLessThanOrEqual(4);
      expect(new Set(chips).size).toBe(chips.length);
    }
  });
});
