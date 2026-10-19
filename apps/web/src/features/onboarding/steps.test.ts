import { describe, expect, it } from "vitest";
import type { OnboardingStep } from "../../shared/api/endpoints";
import { blockers, firstOpenStep, nextStep, orderedSteps, requiredProgress, stepLock, stepStatusChip, type LockState } from "./steps";

const api = (over: Partial<Record<OnboardingStep["key"], OnboardingStep["status"]>> = {}): OnboardingStep[] =>
  (["store_profile", "catalog_setup", "opening_stock", "opening_debt", "opening_cash", "first_sale", "confirm_opening_balances"] as const).map((key) => ({
    key,
    status: over[key] ?? "pending",
    skippable: key === "catalog_setup" || key === "first_sale",
    completed_at: null,
    skipped_at: null,
  }));

describe("onboarding steps", () => {
  it("shows the confirmation before the first sale, grouped in three phases", () => {
    const steps = orderedSteps(api());
    expect(steps.map((s) => s.key)).toEqual(["store_profile", "catalog_setup", "opening_stock", "opening_debt", "opening_cash", "confirm_opening_balances", "first_sale"]);
    expect(steps.map((s) => s.phase)).toEqual([0, 0, 1, 1, 1, 1, 2]);
    expect(steps.find((s) => s.key === "confirm_opening_balances")?.label).toBe("Xác nhận và khóa số dư");
  });

  it("counts the five steps the owner may not skip", () => {
    expect(requiredProgress(orderedSteps(api({ store_profile: "completed", catalog_setup: "completed" })))).toEqual({ done: 1, total: 5 });
  });

  it("opens the first pending step in display order", () => {
    expect(firstOpenStep(orderedSteps(api({ store_profile: "completed", catalog_setup: "skipped" })))).toBe("opening_stock");
  });

  it("locks the opening steps without a cutoff and after the confirmation, and the first sale until then", () => {
    const none = { cutoff: null, confirmed: false };
    const set = { cutoff: "2026-09-30", confirmed: false };
    const done = { cutoff: "2026-09-30", confirmed: true };
    expect(stepLock("opening_stock", none)).toBe("Cần ngày chốt ở bước 1");
    expect(stepLock("confirm_opening_balances", none)).toBe("Cần ngày chốt ở bước 1");
    expect(stepLock("opening_cash", set)).toBe("");
    expect(stepLock("opening_debt", done)).toBe("Đã khóa sau xác nhận");
    expect(stepLock("confirm_opening_balances", done)).toBe("");
    expect(stepLock("first_sale", set)).toBe("Mở sau khi xác nhận số dư");
    expect(stepLock("first_sale", done)).toBe("");
    expect(stepLock("store_profile", none)).toBe("");
  });

  it("names the steps the confirmation still needs", () => {
    expect(blockers(orderedSteps(api({ store_profile: "completed", opening_debt: "completed" })))).toEqual(["opening_stock", "opening_cash"]);
  });

  it("chips a pending step with its lock, else its status", () => {
    const steps = orderedSteps(api({ store_profile: "completed", catalog_setup: "skipped" }));
    const lock = { cutoff: null, confirmed: false };
    const chip = (i: number, at: LockState = lock) => {
      const step = steps[i];
      if (!step) throw new Error(`no step ${i}`);
      return stepStatusChip(step, at);
    };
    expect(chip(0)).toEqual({ label: "Đã xong", tone: "ok" });
    expect(chip(1)).toEqual({ label: "Đã bỏ qua", tone: "mute" });
    expect(chip(2)).toEqual({ label: "Cần ngày chốt ở bước 1", tone: "mute" });
    expect(chip(2, { cutoff: "2026-09-30", confirmed: false })).toEqual({ label: "Chưa làm", tone: null });
  });

  it("moves to the next step in display order", () => {
    expect(nextStep("opening_cash")).toBe("confirm_opening_balances");
    expect(nextStep("confirm_opening_balances")).toBe("first_sale");
    expect(nextStep("first_sale")).toBe("first_sale");
  });
});
