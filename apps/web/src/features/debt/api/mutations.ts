import { createElement, Fragment, useRef, type ReactNode } from "react";
import type { QueryKey } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import type { Money } from "../../../shared/domain/money";
import { createOnce, newCreateState, useUnsettled, type CreateState } from "../../../shared/hooks/create-once";
import { PartialCommitError, useCommit } from "../../../shared/hooks/use-commit";
import { etagOf } from "../../catalog";
import { supplierKeys } from "../../partners";
import { postedAdjustmentSchema, postedPaymentSchema, statementSchema, type DebtDirection, type Statement } from "../schemas";
import { SIDE, withDebtMessage } from "./adapters";
import { debtKeys } from "./queries";

// Write side of the Công nợ screens. Payments, allocations, debt adjustments, collection notes and new
// statements go through useCommit with one Idempotency-Key per intent; the commands on an existing
// statement take If-Match and no key (OpenAPI). A write-off asks for the password (403
// reauth_required) and then the owner's confirmation of the debt_write_off exception, both handled
// by useCommit.

/** What a payment or adjustment may change: balances, funds, the dashboard, documents' paid state, reports. */
const POSTING_KEYS: readonly QueryKey[] = [debtKeys.all, ["cash"], ["dashboard"], ["sales"], ["reports"], supplierKeys.all];

/** useCommit's setFieldError: a dialog marks the input a refused request field belongs to. */
type OnFieldError = (field: string, message: string) => void;

/** Toast text with the document number in bold: `before <b>docNo</b> after`. */
const withDoc = (before: string, docNo: string, after: string): ReactNode => createElement(Fragment, null, before, createElement("b", null, docNo), after);

/** Runs a request and maps a refusal onto the prototype sentence. */
async function debtCall<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (e) {
    throw withDebtMessage(e);
  }
}

export interface AllocationLineVars {
  obligationId: string;
  amount: Money;
  /** The line still holds the oldest-due-first suggestion. */
  suggested: boolean;
}

const allocationBody = (lines: readonly AllocationLineVars[]) =>
  lines.map((l) => ({ obligation_id: l.obligationId, amount: l.amount, mode: l.suggested ? "suggested_oldest_due" : "manual" }));

/* ---------- payments ---------- */

export interface PayVars {
  direction: DebtDirection;
  partyId: string;
  amount: Money;
  paymentMethodId: string;
  note: string;
  allocations: AllocationLineVars[];
}

/** payModal: a PT (customer pays) or PC (store pays a supplier), spread over open documents. */
export function usePay(after: () => void, onFieldError?: OnFieldError) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: PayVars, { idempotencyKey }) => {
      const body = {
        direction: SIDE[v.direction].paymentDirection,
        party_id: v.partyId,
        // Nothing to allocate: the whole amount is an advance.
        purpose: v.allocations.length > 0 ? "settlement" : "advance",
        payment_method_id: v.paymentMethodId,
        amount: v.amount,
        source_type: "debt",
        ...(v.note.trim() !== "" ? { note: v.note.trim() } : {}),
        ...(v.allocations.length > 0 ? { allocations: allocationBody(v.allocations) } : {}),
      };
      return debtCall(() => client.post("/payments", body, { idempotencyKey, schema: postedPaymentSchema }));
    },
    success: (p) => withDoc("Đã ghi ", p.doc_no, "."),
    invalidate: POSTING_KEYS,
    after,
    ...(onFieldError ? { setFieldError: onFieldError } : {}),
  });
}

export interface AllocateVars {
  paymentId: string;
  allocations: AllocationLineVars[];
}

/** allocAdv: spreads what is left of an advance over open documents of the same party. */
export function useAllocate(after: () => void, onFieldError?: OnFieldError) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: AllocateVars, { idempotencyKey }) =>
      debtCall(() => client.post("/allocations", { payment_id: v.paymentId, allocations: allocationBody(v.allocations) }, { idempotencyKey, schema: postedPaymentSchema })),
    success: () => "Đã phân bổ.",
    invalidate: POSTING_KEYS,
    after,
    ...(onFieldError ? { setFieldError: onFieldError } : {}),
  });
}

/* ---------- write-off ---------- */

export interface WriteOffVars {
  partyId: string;
  direction: DebtDirection;
  obligation: { id: string; docNo: string; version: number };
  amount: Money;
  reasonId: string;
  note: string;
}

const writeOffBody = (v: WriteOffVars) => ({
  party_id: v.partyId,
  direction: v.direction,
  kind: "write_off",
  amount: v.amount,
  target_obligation_id: v.obligation.id,
  reason_id: v.reasonId,
  ...(v.note.trim() !== "" ? { note: v.note.trim() } : {}),
});

/** writeOff: the owner's decision to forgive part of a document, after the password and the exception. */
export function useWriteOff(after: () => void, onFieldError?: OnFieldError) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: WriteOffVars, { idempotencyKey, exceptionConfirmationId }) =>
      debtCall(() =>
        client.post("/debt-adjustments", { ...writeOffBody(v), ...(exceptionConfirmationId ? { exception_confirmation_id: exceptionConfirmationId } : {}) }, { idempotencyKey, schema: postedAdjustmentSchema }),
      ),
    exception: (e, v) =>
      e.exceptionKind === "debt_write_off"
        ? {
            kind: e.exceptionKind,
            subject: { doc_type: "obligation", doc_id: v.obligation.id, doc_version: v.obligation.version, impact: { amount: v.amount } },
            payload: writeOffBody(v),
            title: `Xác nhận xóa nợ ${v.obligation.docNo}`,
            sub: "Giảm/xóa nợ là quyết định của chủ cửa hàng, ghi nhật ký kèm lý do; không phải tiền thu và không làm tăng doanh thu.",
            checkboxLabel: "Tôi xác nhận ghi giảm nợ",
            submitLabel: "Ghi giảm nợ",
          }
        : null,
    success: () => "Đã ghi giảm nợ.",
    invalidate: POSTING_KEYS,
    after,
    ...(onFieldError ? { setFieldError: onFieldError } : {}),
  });
}

/* ---------- collection notes ---------- */

export interface NoteVars {
  partyId: string;
  direction: DebtDirection;
  body: string;
  promisedDate: string | null;
}

/** debtNote: a contact note, a promise when the customer named a date (`POST /parties/{id}/collection-notes`). */
export function useCollectionNote(after: () => void, onFieldError?: OnFieldError) {
  const client = useApiClient();
  const creating = useRef<{ key: string; state: CreateState<object, unknown> } | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: NoteVars, { idempotencyKey }) => {
      if (creating.current?.key !== idempotencyKey) creating.current = { key: idempotencyKey, state: newCreateState() };
      const create = creating.current.state;
      const body = {
        direction: v.direction,
        kind: v.promisedDate ? "promise" : "call",
        body: v.body.trim(),
        ...(v.promisedDate ? { promised_date: v.promisedDate } : {}),
        channel: "phone",
      };
      // A body whose answer was lost goes out again first: the key with another body is refused (409).
      const made = await lost.watch(create, () => debtCall(() => createOnce(create, body, { send: (b) => client.post<unknown>(`/parties/${v.partyId}/collection-notes`, b, { idempotencyKey }) })));
      if (made.edited) throw new PartialCommitError("Đã lưu ghi chú của lần bấm trước, lần đó chưa nhận được trả lời. Nội dung sửa sau đó chưa được lưu.");
      return made.result;
    },
    success: () => "Đã lưu ghi chú.",
    invalidate: (_d, v) => [debtKeys.notes(v.partyId), debtKeys.forecasts],
    after,
    ...(onFieldError ? { setFieldError: onFieldError } : {}),
  });
  return { ...commit, unsettled: lost.unsettled };
}

/* ---------- statements ---------- */

export interface NewStatementVars {
  partyId: string;
  direction: DebtDirection;
  from: string;
  to: string;
}

/** newSTM: snapshots the party's ledger over a period as version 1 (`POST /statements`). */
export function useCreateStatement(after: (s: Statement) => void, onFieldError?: OnFieldError) {
  const client = useApiClient();
  const creating = useRef<{ key: string; state: CreateState<object, Statement> } | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: NewStatementVars, { idempotencyKey }) => {
      if (creating.current?.key !== idempotencyKey) creating.current = { key: idempotencyKey, state: newCreateState() };
      const create = creating.current.state;
      const body = { party_id: v.partyId, direction: v.direction, period_from: v.from, period_to: v.to };
      const made = await lost.watch(create, () => debtCall(() => createOnce(create, body, { send: (b) => client.post("/statements", b, { idempotencyKey, schema: statementSchema }) })));
      if (made.edited) throw new PartialCommitError(withDoc("Đã lập ", made.result.doc_no, " theo kỳ của lần bấm trước, lần đó chưa nhận được trả lời. Kỳ vừa sửa chưa được lập."));
      return made.result;
    },
    success: (s) => withDoc("Đã lập ", s.doc_no, "."),
    invalidate: [debtKeys.allStatements],
    after,
    ...(onFieldError ? { setFieldError: onFieldError } : {}),
  });
  return { ...commit, unsettled: lost.unsettled };
}

export type StatementCommand = "send" | "confirm" | "generate";

const COMMAND_TOASTS: Readonly<Record<StatementCommand, string>> = {
  send: "Đã gửi bảng kê. Bản đã gửi không bị thay đổi.",
  confirm: "Khách đã xác nhận số dư.",
  generate: "Đã lập phiên bản mới.",
};

/** "Gửi khách", "Khách xác nhận" and "Lập phiên bản mới" on a statement (If-Match its version). */
export function useStatementCommand() {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { statement: Statement; command: StatementCommand }) =>
      debtCall(() => client.post(`/statements/${v.statement.id}:${v.command}`, {}, { ifMatch: etagOf(v.statement.version), schema: statementSchema })),
    success: (_s, v) => COMMAND_TOASTS[v.command],
    invalidate: [debtKeys.allStatements],
  });
}

/** Dispute dialog: records what the customer contests (`:dispute`). */
export function useDisputeStatement(after: () => void, onFieldError?: OnFieldError) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { statement: Statement; note: string }) =>
      debtCall(() => client.post(`/statements/${v.statement.id}:dispute`, { note: v.note.trim() }, { ifMatch: etagOf(v.statement.version), schema: statementSchema })),
    success: () => "Đã ghi nhận tranh chấp. Lập phiên bản mới sau khi điều chỉnh.",
    invalidate: [debtKeys.allStatements],
    after,
    ...(onFieldError ? { setFieldError: onFieldError } : {}),
  });
}
