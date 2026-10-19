import { useMemo, useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { money, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { MoneyInput } from "../../../shared/ui/money-input";
import { byOldestDue, suggestAllocation } from "../allocation";
import { DEBT_MESSAGES } from "../api/adapters";
import { useAllocate } from "../api/mutations";
import { usePartyLedger } from "../api/queries";
import { useAllocationErrors } from "../field-errors";
import type { DebtDirection, LedgerDocument, Payment } from "../schemas";

export interface AllocateAdvanceDialogProps {
  payment: Payment;
  partyName: string;
  onClose: () => void;
}

/** Prototype allocAdv: what is left of an advance spread over the same party's open documents. */
export function AllocateAdvanceDialog({ payment, partyName, onClose }: AllocateAdvanceDialogProps) {
  const direction: DebtDirection = payment.direction === "in" ? "receivable" : "payable";
  const ledger = usePartyLedger(payment.party_id ?? null);
  const [edits, setEdits] = useState<ReadonlyMap<string, Money | null>>(new Map());
  const lineErrors = useAllocationErrors();
  const allocate = useAllocate(onClose, lineErrors.onFieldError);
  // A check of the dialog's own that failed, with the line it is about (marked and focused).
  const [problem, setProblem] = useState<{ message: string; docId: string | null } | null>(null);
  const focusFirstInvalid = useFocusFirstInvalid();
  const fail = (message: string, docId: string | null) => {
    setProblem({ message, docId });
    focusFirstInvalid();
  };

  const docs = useMemo(() => byOldestDue((ledger.data?.documents ?? []).filter((d) => d.direction === direction)), [ledger.data, direction]);
  const suggestion = useMemo(() => suggestAllocation(payment.unallocated, docs), [payment.unallocated, docs]);
  const amountOf = (d: LedgerDocument): Money | null => (edits.has(d.id) ? (edits.get(d.id) ?? null) : (suggestion.get(d.id) ?? null));

  const submit = () => {
    const lines = docs.flatMap((d) => {
      const a = amountOf(d);
      return a !== null && dec(a).gt(0) ? [{ doc: d, amount: a }] : [];
    });
    if (lines.length === 0) return fail(DEBT_MESSAGES.allocationEmpty, docs[0]?.id ?? null);
    if (sumDec(lines.map((l) => l.amount)).gt(payment.unallocated)) return fail(DEBT_MESSAGES.advanceOver, null);
    const over = lines.find((l) => dec(l.amount).gt(l.doc.open));
    if (over !== undefined) return fail(DEBT_MESSAGES.advanceOverOpen(over.doc.source_doc_no), over.doc.id);
    setProblem(null);
    lineErrors.sending(lines.map((l) => l.doc.id));
    void allocate.run({
      paymentId: payment.id,
      allocations: lines.map((l) => ({ obligationId: l.doc.id, amount: l.amount, suggested: !edits.has(l.doc.id) })),
    });
  };

  const columns: Column<LedgerDocument>[] = [
    { key: "doc", header: "Chứng từ", cell: (d) => <DocNo value={d.source_doc_no} /> },
    { key: "open", header: "Còn nợ", numeric: true, cell: (d) => money(d.open) },
    {
      key: "alloc",
      header: "Phân bổ",
      numeric: true,
      cell: (d) => (
        <MoneyInput
          aria-label={`Phân bổ ${d.source_doc_no}`}
          size="md"
          value={amountOf(d)}
          invalid={lineErrors.errors.has(d.id) || problem?.docId === d.id}
          onChange={(v) => {
            setEdits((cur) => new Map(cur).set(d.id, v));
            lineErrors.clear(d.id);
            setProblem(null);
          }}
        />
      ),
    },
  ];

  const refused = docs.filter((d) => lineErrors.errors.has(d.id));

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Phân bổ ${payment.doc_no}`}
      sub={`Còn ${vnd(payment.unallocated)} chưa phân bổ của ${partyName}.`}
      submitLabel="Ghi phân bổ"
      onSubmit={submit}
      pending={allocate.pending}
      live={
        <>
          {problem ? (
            <Callout tone="bad" role="alert">
              {problem.message}
            </Callout>
          ) : null}
          {refused.length > 0 ? (
            <Callout tone="bad" role="alert">
              {refused.map((d) => `${d.source_doc_no}: ${lineErrors.errors.get(d.id) ?? ""}`).join(" ")}
            </Callout>
          ) : null}
        </>
      }
    >
      <DataTable caption={`Phân bổ ${payment.doc_no}`} columns={columns} rows={docs} rowKey={(d) => d.id} flat empty="Không có chứng từ còn mở." />
    </CommitDialog>
  );
}
