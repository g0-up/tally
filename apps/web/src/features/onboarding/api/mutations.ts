import { createElement, Fragment, useRef, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage, isApiError, isReauthRequired, isSessionExpired } from "../../../shared/api/api-error";
import { onboardingStepSchema, tenantSchema, type OnboardingStep, type OnboardingStepKey, type Tenant } from "../../../shared/api/endpoints";
import { platformKeys } from "../../../shared/api/query-keys";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { PartialCommitError, useCommit } from "../../../shared/hooks/use-commit";
import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
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
  /** Overrides the default success toast. */
  success?: (data: T) => ReactNode;
}

/* ---------- step commands ---------- */

export type StepAction = "complete" | "skip";

/**
 * Completes or skips one step (`POST /onboarding/steps/{key}:complete|:skip`). A quiet command
 * follows another write that already announced itself.
 */
export function useStepCommand(key: OnboardingStepKey, action: StepAction, { quiet = false, after }: { quiet?: boolean; after?: () => void } = {}) {
  const client = useApiClient();
  const label = stepLabel(key);
  return useCommit<OnboardingStep>({
    mutationFn: () => client.post(`/onboarding/steps/${key}:${action}`, undefined, { schema: onboardingStepSchema }),
    ...(quiet
      ? {}
      : {
          success: () =>
            action === "complete"
              ? createElement(Fragment, null, "Đã xong ", createElement("b", null, label), ".")
              : createElement(Fragment, null, "Đã bỏ qua ", createElement("b", null, label), ". Có thể làm lại bất cứ lúc nào."),
        }),
    // Marking a step posts nothing, so a refusal names the step rather than "Chưa ghi sổ.".
    failedLead: action === "complete" ? `Chưa đánh dấu xong bước “${label}”.` : `Chưa bỏ qua bước “${label}”.`,
    invalidate: CHECKLIST_KEYS,
    ...(after ? { after } : {}),
  });
}

/**
 * Confirms the opening totals and locks every period up to the cutoff. The API asks for the
 * password first (`reauth_required`); `useCommit` opens the prompt, worded for the lock, and retries once.
 */
export function useConfirmOpeningBalances(cutoff: string | null, { after, setFieldError }: CommitHooks<OnboardingStep>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useCommit<OnboardingStep>({
    mutationFn: () => client.post("/onboarding/steps/confirm_opening_balances:complete", undefined, { schema: onboardingStepSchema }),
    success: () => "Đã khóa số dư đầu kỳ. POS đã mở.",
    reauth: { title: "Nhập lại mật khẩu để khóa sổ", sub: `Xác nhận số dư đầu kỳ đến hết ngày ${fd(cutoff)}. Không hoàn tác được.`, submitLabel: "Khóa sổ và xác nhận" },
    // The confirmation locks the books and freezes the cutoff on the store profile.
    invalidate: [settingsKeys.periodLocks(), platformKeys.tenant(), ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
    // A refusal names the steps the API still sees pending: re-read the checklist it contradicts.
    setFieldError: (field, message) => {
      void queryClient.invalidateQueries({ queryKey: platformKeys.onboarding() });
      setFieldError?.(field, message);
    },
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
export function useSaveTenantProfile(profile: Tenant | undefined, { after, setFieldError, success }: CommitHooks<Tenant>) {
  const client = useApiClient();
  return useCommit<Tenant, TenantProfilePatch>({
    mutationFn: (patch) => client.patch("/tenant", patch, { ifMatch: profile ? etagOf(profile.version) : null, schema: tenantSchema }),
    success: success ?? (() => "Đã lưu thông tin cửa hàng."),
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

/** Toast text with one part in bold: `before <b>bold</b>after` */
const boldToast = (before: string, bold: string, after: string): ReactNode => createElement(Fragment, null, before, createElement("b", null, bold), after);

/** One opening stock document (`POST /stock-adjustments`, kind opening), dated on the cutoff. */
export function usePostOpeningStock({ after, setFieldError }: CommitHooks<{ id: string; doc_no: string }>) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: OpeningStockVars, { idempotencyKey }) =>
      client.post("/stock-adjustments", { kind: "opening", business_date: v.businessDate, lines: v.lines }, { idempotencyKey, schema: postedDocSchema }),
    success: (_doc, v) => boldToast("Đã ghi ", `${v.lines.length} dòng`, " tồn kho đầu kỳ. Có thể ghi thêm trước khi khóa sổ."),
    invalidate: [...STOCK_POSTING_KEYS, onboardingKeys.openingStock(), ...CHECKLIST_KEYS],
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
    success: (_doc, v) => boldToast("Đã ghi công nợ đầu kỳ ", vnd(v.amount), ". Ghi tiếp khoản khác hoặc sang bước sau."),
    invalidate: [debtKeys.all, onboardingKeys.openingDebts(), ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
    ...(setFieldError ? { setFieldError } : {}),
  });
}

export interface FundOpeningsVars {
  /** Every fund with the balance entered for it. */
  entries: readonly { fund: FundAccount; opening_balance: Money }[];
  opening_date: string;
}

/**
 * Opening balances of every fund, one `PATCH /funds/{id}` after another under each fund's version
 * (a fund already holding the entered balance is left alone). A failure stops the run and says which
 * funds were saved; a password prompt, an expired session or stale data reach their usual handler.
 */
export function useSaveFundOpenings({ after }: CommitHooks<number>) {
  const client = useApiClient();
  // Funds saved by an earlier pass, so the retry after a password prompt skips them instead of
  // sending their old version.
  const patched = useRef(new Map<string, FundAccount>());
  return useCommit({
    mutationFn: async ({ entries, opening_date }: FundOpeningsVars) => {
      let saved = 0;
      for (const entry of entries) {
        const { opening_balance } = entry;
        const known = patched.current.get(entry.fund.id);
        const fund = known && known.version > entry.fund.version ? known : entry.fund;
        if (fund.opening_date === opening_date && dec(fund.opening_balance).eq(opening_balance)) {
          saved++;
          continue;
        }
        try {
          patched.current.set(fund.id, await client.patch(`/funds/${fund.id}`, { opening_balance, opening_date }, { ifMatch: etagOf(fund.version), schema: fundAccountSchema }));
        } catch (e) {
          if (isReauthRequired(e) || isSessionExpired(e) || (isApiError(e) && e.status === 412)) throw e;
          throw new PartialCommitError(
            saved > 0
              ? boldToast(`Đã lưu ${saved} quỹ, chưa lưu `, fund.name, `. ${errorMessage(e)}`)
              : boldToast("Chưa lưu ", fund.name, `. ${errorMessage(e)}`),
          );
        }
        saved++;
      }
      return saved;
    },
    success: (n) => `Đã lưu số dư ${n} quỹ.`,
    invalidate: [onboardingKeys.funds(), debtKeys.funds, ...CHECKLIST_KEYS],
    ...(after ? { after } : {}),
  });
}
