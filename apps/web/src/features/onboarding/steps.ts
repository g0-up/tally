import { ONBOARDING_STEP_KEYS, type OnboardingStep, type OnboardingStepKey, type OnboardingStepStatus } from "../../shared/api/endpoints";

/** Prototype `ONB_PH`: the rail groups. */
export const ONBOARDING_PHASES = ["Cửa hàng", "Số dư đầu kỳ", "Chạy thử"] as const;

interface StepInfo {
  key: OnboardingStepKey;
  phase: 0 | 1 | 2;
  label: string;
  note: string;
}

/**
 * Prototype `ONB_STEPS`, in the order the owner works through them. The API lists the first sale
 * before the confirmation; the screen shows the confirmation first because the POS opens only after it.
 */
const STEP_INFO: readonly StepInfo[] = [
  { key: "store_profile", phase: 0, label: "Thông tin cửa hàng", note: "Tên và mã số thuế in trên hóa đơn. Ngày chốt quyết định số dư đầu kỳ của mọi bước sau." },
  { key: "catalog_setup", phase: 0, label: "Danh mục hàng hóa", note: "Thêm các mặt hàng chính để nhập tồn đầu kỳ. Danh mục đầy đủ có thể làm sau trong Danh mục." },
  { key: "opening_stock", phase: 1, label: "Tồn kho đầu kỳ", note: "Số lượng và giá vốn hàng còn trong kho tính đến hết ngày chốt." },
  { key: "opening_debt", phase: 1, label: "Công nợ đầu kỳ", note: "Khoản khách còn nợ cửa hàng và cửa hàng còn nợ nhà cung cấp tại ngày chốt. Ghi từng khoản." },
  { key: "opening_cash", phase: 1, label: "Tiền đầu kỳ", note: "Tiền thực có trong từng quỹ tại ngày chốt. Nhập 0 nếu quỹ chưa có tiền." },
  { key: "confirm_opening_balances", phase: 1, label: "Xác nhận và khóa số dư", note: "Kiểm tra tổng số dư rồi khóa sổ. Sau bước này, POS và các màn bán hàng được mở." },
  { key: "first_sale", phase: 2, label: "Bán đơn đầu tiên", note: "Bán thử một đơn để kiểm tra giá, tồn kho và hóa đơn. Bước này tự hoàn tất khi POS có hóa đơn đầu tiên." },
];

const INFO_BY_KEY = new Map(STEP_INFO.map((s) => [s.key, s]));

/** Steps the API lets the owner skip, for a step its answer does not list yet. */
const SKIPPABLE_FALLBACK: ReadonlySet<OnboardingStepKey> = new Set(["catalog_setup", "first_sale"]);

/** Balances entered as of the cutoff date, and the confirmation that freezes them. */
const OPENING_STEPS: ReadonlySet<OnboardingStepKey> = new Set(["opening_stock", "opening_debt", "opening_cash", "confirm_opening_balances"]);

/** Steps the confirmation needs completed (prototype `obBlockers`). */
const CONFIRM_NEEDS: readonly OnboardingStepKey[] = ["store_profile", "opening_stock", "opening_debt", "opening_cash"];

export interface ChecklistStep {
  key: OnboardingStepKey;
  status: OnboardingStepStatus;
  /** The owner may skip it; a step that may not be skipped is required. */
  skippable: boolean;
  /** Rail group, index into `ONBOARDING_PHASES`. */
  phase: 0 | 1 | 2;
  label: string;
  note: string;
  completed_at: string | null;
}

/** What locks a step: the cutoff date not chosen yet, and the confirmation. */
export interface LockState {
  cutoff: string | null;
  confirmed: boolean;
}

export function stepLabel(key: OnboardingStepKey): string {
  return INFO_BY_KEY.get(key)?.label ?? key;
}

/** The API's steps in display order; a step it does not list yet counts as pending. */
export function orderedSteps(steps: readonly OnboardingStep[]): ChecklistStep[] {
  const byKey = new Map(steps.map((s) => [s.key, s]));
  return STEP_INFO.map(({ key, phase, label, note }) => {
    const s = byKey.get(key);
    return { key, phase, label, note, status: s?.status ?? "pending", skippable: s?.skippable ?? SKIPPABLE_FALLBACK.has(key), completed_at: s?.completed_at ?? null };
  });
}

/** The step to open first: the first pending one, else the first step. */
export function firstOpenStep(steps: readonly ChecklistStep[]): OnboardingStepKey {
  return (steps.find((s) => s.status === "pending") ?? steps[0])?.key ?? "store_profile";
}

/** Required steps completed and their count (prototype `obReqDone` and the page meter). */
export function requiredProgress(steps: readonly ChecklistStep[]): { done: number; total: number } {
  const required = steps.filter((s) => !s.skippable);
  return { done: required.filter((s) => s.status === "completed").length, total: required.length };
}

/** Every step the owner may not skip is completed. */
export function requiredStepsDone(steps: readonly ChecklistStep[]): boolean {
  const { done, total } = requiredProgress(steps);
  return done === total;
}

/** Prototype `obLock`: why a step cannot be worked on now, or "" when it can. */
export function stepLock(key: OnboardingStepKey, { cutoff, confirmed }: LockState): string {
  if (OPENING_STEPS.has(key)) {
    if (confirmed) return key === "confirm_opening_balances" ? "" : "Đã khóa sau xác nhận";
    if (cutoff === null) return "Cần ngày chốt ở bước 1";
  }
  if (key === "first_sale" && !confirmed) return "Mở sau khi xác nhận số dư";
  return "";
}

/** Steps still pending before the opening balances can be confirmed. */
export function blockers(steps: readonly ChecklistStep[]): OnboardingStepKey[] {
  return CONFIRM_NEEDS.filter((key) => steps.find((s) => s.key === key)?.status !== "completed");
}

/** Prototype `obStatusChip`: the step's status, or its lock while pending. A null tone is the plain `.st`. */
export function stepStatusChip(step: ChecklistStep, lock: LockState): { label: string; tone: "ok" | "mute" | null } {
  if (step.status === "completed") return { label: "Đã xong", tone: "ok" };
  if (step.status === "skipped") return { label: "Đã bỏ qua", tone: "mute" };
  const lk = stepLock(step.key, lock);
  return lk ? { label: lk, tone: "mute" } : { label: "Chưa làm", tone: null };
}

/** The step after this one in display order, or the same step when it is the last. */
export function nextStep(key: OnboardingStepKey): OnboardingStepKey {
  const i = STEP_INFO.findIndex((s) => s.key === key);
  return (STEP_INFO[i + 1] ?? STEP_INFO[i])?.key ?? key;
}

export function isOnboardingStepKey(value: string): value is OnboardingStepKey {
  return (ONBOARDING_STEP_KEYS as readonly string[]).includes(value);
}
