import { createContext, useContext, type ReactNode } from "react";

// A command the API refuses with 409 + exception_kind may still go through when the owner
// confirms the exception explicitly. The confirmation is stored by the API
// (POST /exception-confirmations), bound to the document version and to the hash of the exact
// command body, and the command is retried with `exception_confirmation_id`.
//
// `useConfirmException()` opens the shared ExceptionConfirmDialog and resolves with the stored
// confirmation id, or null when the owner cancels. The provider lives in
// `shared/ui/exception-confirm-dialog.tsx` and is mounted once by the app.

export interface ExceptionIssue {
  /** Short rule name, rendered bold ("Vượt hạn mức nợ"). */
  type: string;
  detail: string;
}

/** What the confirmation is bound to on the API side. */
export interface ExceptionSubject {
  doc_type: string;
  doc_id: string;
  /** Version of the document the owner saw (0 for a document being created). */
  doc_version: number;
  /** Optional figures recorded with the confirmation for the audit log. */
  impact?: Record<string, unknown>;
  note?: string;
}

export interface ExceptionPrompt {
  /** `exception_kind` from the 409 answer. */
  kind: string;
  subject: ExceptionSubject;
  /** Command body that will be retried; its canonical hash is part of the confirmation. */
  payload: unknown;
  title: ReactNode;
  sub?: ReactNode;
  /** Defaults to one issue built from the kind label and the API message. */
  issues?: readonly ExceptionIssue[];
  checkboxLabel?: string;
  submitLabel?: string;
}

export type ConfirmException = (prompt: ExceptionPrompt) => Promise<string | null>;

export const ConfirmExceptionContext = createContext<ConfirmException | null>(null);

export const EXCEPTION_SUB = "Hóa đơn vượt kiểm soát. Bạn là người quyết định; lựa chọn được ghi vào nhật ký.";
export const EXCEPTION_CHECKBOX = "Tôi xác nhận vẫn bán";
export const EXCEPTION_SUBMIT = "Xác nhận bán";

/** Vietnamese names of the exception kinds the API knows. */
export const EXCEPTION_KIND_LABELS: Readonly<Record<string, string>> = {
  credit_limit_exceeded: "Vượt hạn mức nợ",
  below_min_price: "Dưới giá tối thiểu",
  stock_disposal: "Tiêu hủy hàng",
  count_variance: "Chênh lệch kiểm kê",
  debt_write_off: "Xóa nợ",
  expired_lot_sale: "Bán lô hết hạn",
  quarantine_release: "Giải phóng hàng cách ly",
  period_reopen: "Mở lại kỳ đã khóa",
};

export function exceptionKindLabel(kind: string): string {
  return EXCEPTION_KIND_LABELS[kind] ?? "Ngoại lệ";
}

/** Issue list used when the caller has no figures of its own to show. */
export function defaultExceptionIssues(kind: string): ExceptionIssue[] {
  return [{ type: exceptionKindLabel(kind), detail: "cần bạn xác nhận để tiếp tục." }];
}

export function exceptionTitle(docNo: string): string {
  return `Xác nhận ngoại lệ cho ${docNo}`;
}

/** Message shown when the owner submits without ticking the confirmation box. */
export function exceptionUncheckedMessage(checkboxLabel: string): string {
  return `Tích “${checkboxLabel}” để tiếp tục.`;
}

export function useConfirmException(): ConfirmException {
  const confirm = useContext(ConfirmExceptionContext);
  if (!confirm) throw new Error("useConfirmException must be used inside ExceptionConfirmProvider");
  return confirm;
}
