import { createElement, Fragment, type ReactNode } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { onboardingStepSchema, tenantSchema, type OnboardingStep, type OnboardingStepKey, type Tenant } from "../../../shared/api/endpoints";
import { platformKeys } from "../../../shared/api/query-keys";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fd } from "../../../shared/lib/format";
import { etagOf } from "../../catalog";
import { debtKeys } from "../../debt";
import { STOCK_POSTING_KEYS } from "../../inventory";
import { settingsKeys } from "../../settings";
import { fundAccountSchema, postedDocSchema, type FundAccount } from "../schemas";
import { stepLabel } from "../steps";
import { onboardingKeys } from "./queries";

// Write side of the onboarding screen. Every write refreshes the checklist (the cache the screen
// and the shell's gate share), since a posting can complete a step on the server.

const CHECKLIST_KEYS = [platformKeys.onboarding()] as const;

interface CommitHooks<T> {
  after?: (data: T) => void;
  setFieldError?: (field: string, message: string) => void;
}

/* ---------- step commands ---------- */

export type StepAction = "complete" | "skip";

/**
 * "Đánh dấu xong" / "Bỏ qua" of one step (`POST /onboarding/steps/{key}:complete|:skip`). A quiet
 * command follows another write that already announced itself.
 */
export function useStepCommand(key: OnboardingStepKey, action: StepAction, { quiet = false }: { quiet?: boolean } = {}) {
  const client = useApiClient();
  const label = stepLabel(key);
  return useCommit<OnboardingStep>({
    mutationFn: () => client.post(`/onboarding/steps/${key}:${action}`, undefined, { schema: onboardingStepSchema }),
    ...(quiet ? {} : { success: () => (action === "complete" ? `Đã xong bước “${label}”.` : `Đã bỏ qua bước “${label}”.`) }),
    // Marking a step posts nothing, so a refusal names the step rather than "Chưa ghi sổ.".
    failedLead: action === "complete" ? `Chưa đánh dấu xong bước “${label}”.` : `Chưa bỏ qua bước “${label}”.`,
    invalidate: CHECKLIST_KEYS,
  });
}

/**
 * Confirms the opening totals and locks every period up to the cutoff. The API asks for the
 * password first (`reauth_required`); `useCommit` opens the prompt and retries once.
 */
export function useConfirmOpeningBalances({ after, setFieldError }: CommitHooks<OnboardingStep>) {
  const client = useApiClient();
  return useCommit<OnboardingStep>({
    mutationFn: () => client.post("/onboarding/steps/confirm_opening_balances:complete", undefined, { schema: onboardingStepSchema }),
    success: (step) => (step.totals ? `Đã xác nhận số dư đầu kỳ. Sổ sách đến hết ngày ${fd(step.totals.cutoff)} đã khóa.` : "Đã xác nhận số dư đầu kỳ."),
    // The confirmation locks the books and freezes the cutoff on the store profile.
    invalidate: [settingsKeys.periodLocks(), platformKeys.tenant(), ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
    ...(setFieldError ? { setFieldError } : {}),
  });
}

/* ---------- store profile ---------- */

export interface TenantProfilePatch {
  legal_name: string;
  display_name: string;
  tax_code: string | null;
  address: string | null;
  phone: string | null;
  /** Left out once the opening balances are confirmed: the cutoff is frozen then. */
  opening_cutoff_date?: string;
}

/** `PATCH /tenant` under If-Match of the version read. */
export function useSaveTenantProfile(profile: Tenant | undefined, { after, setFieldError }: CommitHooks<Tenant>) {
  const client = useApiClient();
  return useCommit<Tenant, TenantProfilePatch>({
    mutationFn: (patch) => client.patch("/tenant", patch, { ifMatch: profile ? etagOf(profile.version) : null, schema: tenantSchema }),
    success: () => "Đã lưu thông tin cửa hàng.",
    invalidate: [platformKeys.tenant(), platformKeys.me(), settingsKeys.owner(), ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
    ...(setFieldError ? { setFieldError } : {}),
  });
}

/* ---------- opening balances ---------- */

export interface OpeningStockLine {
  sku_id: string;
  qty: Qty;
  unit_cost: Money;
  color_id?: string;
  lot_no?: string;
  expiry_date?: string;
}

export interface OpeningStockVars {
  businessDate: string;
  lines: OpeningStockLine[];
}

/** Toast text with the document number in bold: `before <b>docNo</b>.` */
const docToast = (before: string, docNo: string): ReactNode => createElement(Fragment, null, `${before} `, createElement("b", null, docNo), ".");

/** One opening stock document (`POST /stock-adjustments`, kind opening), dated on the cutoff. */
export function usePostOpeningStock({ after, setFieldError }: CommitHooks<{ id: string; doc_no: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: OpeningStockVars, { idempotencyKey }) =>
      client.post("/stock-adjustments", { kind: "opening", business_date: v.businessDate, lines: v.lines }, { idempotencyKey, schema: postedDocSchema }),
    success: (doc) => docToast("Đã ghi tồn kho đầu kỳ", doc.doc_no),
    invalidate: [...STOCK_POSTING_KEYS, ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
    ...(setFieldError ? { setFieldError } : {}),
  });
}

export interface OpeningDebtVars {
  party_id: string;
  direction: "receivable" | "payable";
  amount: Money;
  reason_id: string;
  opening_cutoff_date: string;
  due_date?: string;
  note?: string;
}

/** One opening balance of a customer or supplier (`POST /debt-adjustments`, kind opening_balance). */
export function usePostOpeningDebt({ after, setFieldError }: CommitHooks<{ id: string; doc_no: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: OpeningDebtVars, { idempotencyKey }) =>
      client.post("/debt-adjustments", { ...v, kind: "opening_balance" }, { idempotencyKey, schema: postedDocSchema }),
    success: (doc) => docToast("Đã ghi công nợ đầu kỳ", doc.doc_no),
    invalidate: [debtKeys.all, ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
    ...(setFieldError ? { setFieldError } : {}),
  });
}

export interface FundOpeningVars {
  fund: FundAccount;
  opening_balance: Money;
  opening_date: string;
}

/** Opening balance of one fund (`PATCH /funds/{id}` under If-Match); refused once it has entries. */
export function useSaveFundOpening({ after }: CommitHooks<FundAccount>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: FundOpeningVars) =>
      client.patch(`/funds/${v.fund.id}`, { opening_balance: v.opening_balance, opening_date: v.opening_date }, { ifMatch: etagOf(v.fund.version), schema: fundAccountSchema }),
    success: (fund) => `Đã lưu số dư đầu kỳ của ${fund.name}.`,
    invalidate: [onboardingKeys.funds(), debtKeys.funds, ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
  });
}
