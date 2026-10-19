import { ONBOARDING_STEP_KEYS, type OnboardingStep, type OnboardingStepKey, type OnboardingStepStatus } from "../../shared/api/endpoints";
import type { StatusTone } from "../../shared/domain/status-tone";

export const ONBOARDING_STEP_LABELS: Readonly<Record<OnboardingStepKey, string>> = {
  store_profile: "Thông tin cửa hàng",
  catalog_setup: "Danh mục hàng hóa",
  opening_stock: "Tồn kho đầu kỳ",
  opening_debt: "Công nợ đầu kỳ",
  opening_cash: "Tiền đầu kỳ",
  first_sale: "Bán đơn đầu tiên",
  confirm_opening_balances: "Xác nhận số dư đầu kỳ",
};

export const STEP_STATUS_LABELS: Readonly<Record<OnboardingStepStatus, string>> = {
  completed: "Đã xong",
  skipped: "Bỏ qua",
  pending: "Chưa làm",
};

export const STEP_STATUS_TONES: Readonly<Record<OnboardingStepStatus, StatusTone>> = {
  completed: "ok",
  skipped: "mute",
  pending: "warn",
};

/** Steps the API lets the owner skip, for a step its answer does not list yet. */
const SKIPPABLE_FALLBACK: ReadonlySet<OnboardingStepKey> = new Set(["catalog_setup", "first_sale"]);

/** Steps that complete themselves (the first POS sale) or through their own command (the confirmation). */
const NOT_MANUAL: ReadonlySet<OnboardingStepKey> = new Set(["first_sale", "confirm_opening_balances"]);

/** Opening balances the confirmation freezes. */
export const OPENING_STEPS: ReadonlySet<OnboardingStepKey> = new Set(["opening_stock", "opening_debt", "opening_cash"]);

export interface ChecklistStep {
  key: OnboardingStepKey;
  status: OnboardingStepStatus;
  skippable: boolean;
  /** "Đánh dấu xong" applies to this step. */
  manual: boolean;
}

export function stepLabel(key: OnboardingStepKey): string {
  return ONBOARDING_STEP_LABELS[key];
}

/** The API's steps in the canonical order; a step it does not list yet counts as pending. */
export function orderedSteps(steps: readonly OnboardingStep[]): ChecklistStep[] {
  const byKey = new Map(steps.map((s) => [s.key, s]));
  return ONBOARDING_STEP_KEYS.map((key) => {
    const s = byKey.get(key);
    return { key, status: s?.status ?? "pending", skippable: s?.skippable ?? SKIPPABLE_FALLBACK.has(key), manual: !NOT_MANUAL.has(key) };
  });
}

/** Number of steps finished (done or skipped) before the first pending one, for the stepper. */
export function leadingFinished(steps: readonly ChecklistStep[]): number {
  const i = steps.findIndex((s) => s.status === "pending");
  return i === -1 ? steps.length : i;
}

/** The step to open first: the first pending one, else the first step. */
export function firstOpenStep(steps: readonly ChecklistStep[]): OnboardingStepKey {
  return (steps.find((s) => s.status === "pending") ?? steps[0])?.key ?? "store_profile";
}

/** Every step the owner may not skip is completed. */
export function requiredStepsDone(steps: readonly ChecklistStep[]): boolean {
  return steps.every((s) => s.skippable || s.status === "completed");
}

export function isOnboardingStepKey(value: string): value is OnboardingStepKey {
  return (ONBOARDING_STEP_KEYS as readonly string[]).includes(value);
}
