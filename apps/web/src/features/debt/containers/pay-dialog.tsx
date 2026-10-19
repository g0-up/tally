import { useMemo, useState } from "react";
import { toMoney, type Money } from "../../../shared/domain/money";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { fd, money } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { Combobox, type ComboItem } from "../../../shared/ui/combobox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { activeSupplierSource } from "../../partners";
import { byOldestDue, checkAllocation, suggestAllocation } from "../allocation";
import { DEBT_MESSAGES, SIDE } from "../api/adapters";
import { usePay } from "../api/mutations";
import { customerSource, usePartyLedger, usePaymentMethods } from "../api/queries";
import { useAllocationErrors, useFieldErrors } from "../field-errors";
import type { DebtDirection, LedgerDocument } from "../schemas";

export interface PayDialogProps {
  direction: DebtDirection;
  /** The party paying or being paid; null lets the owner search every active customer or supplier. */
  partyId: string | null;
  onClose: () => void;
}

interface AllocRow {
  doc: LedgerDocument;
  amount: Money | null;
  edited: boolean;
}

type PayField = "party" | "amount" | "method" | "note";

/** The request fields the dialog shows a refusal under. */
const API_FIELDS: Readonly<Record<string, PayField>> = { party_id: "party", amount: "amount", payment_method_id: "method", method_id: "method", note: "note" };

/**
 * Prototype payModal: a PT from a customer or a PC to a supplier. The amount is spread over open
 * documents oldest due first; the owner may edit any line, and the lines left untouched re-flow
 * over what remains. Whatever is not allocated becomes an advance.
 */
export function PayDialog({ direction, partyId: fixedParty, onClose }: PayDialogProps) {
  const side = SIDE[direction];
  // Any active party, balance or not: a new customer's deposit is collected here too.
  const [pickedParty, setPickedParty] = useState<Pick<ComboItem, "id" | "name"> | null>(null);
  const partyId = fixedParty ?? pickedParty?.id ?? null;
  const ledger = usePartyLedger(partyId);
  const methods = usePaymentMethods();
  const [pickedMethod, setPickedMethod] = useState<string | null>(null);
  const method = pickedMethod ?? methods.data?.[0]?.id ?? null;
  const [typed, setTyped] = useState<{ value: Money | null } | null>(null);
  const [note, setNote] = useState("");
  const [edits, setEdits] = useState<ReadonlyMap<string, Money | null>>(new Map());
  const fields = useFieldErrors(API_FIELDS);
  const lines = useAllocationErrors();
  const errors = fields.errors;
  const pay = usePay(onClose, (field, message) => fields.onFieldError(field, message) || lines.onFieldError(field, message));

  const docs = useMemo(() => byOldestDue((ledger.data?.documents ?? []).filter((d) => d.direction === direction)), [ledger.data, direction]);
  const openTotal = useMemo(() => toMoney(sumDec(docs.map((d) => d.open))), [docs]);
  // Until the owner types an amount it is everything still owed.
  const amount = typed !== null ? typed.value : ledger.data ? openTotal : null;

  const rows = useMemo<AllocRow[]>(() => {
    const editedSum = sumDec([...edits.values()].map((v) => v ?? "0"));
    const left = amount === null ? null : toMoney(dec(amount).minus(editedSum));
    const suggestion = suggestAllocation(left !== null && dec(left).gt(0) ? left : null, docs.filter((d) => !edits.has(d.id)));
    return docs.map((doc) => (edits.has(doc.id) ? { doc, amount: edits.get(doc.id) ?? null, edited: true } : { doc, amount: suggestion.get(doc.id) ?? null, edited: false }));
  }, [docs, edits, amount]);
  const check = checkAllocation(
    amount,
    rows.map((r) => ({ id: r.doc.id, open: r.doc.open, amount: r.amount })),
  );
  const blocked = check.overPayment || check.overOpen.length > 0;

  const pickParty = (item: ComboItem) => {
    setPickedParty({ id: item.id, name: item.name });
    setEdits(new Map());
    setTyped(null);
    lines.sending([]);
    fields.clear("party");
  };

  const submit = () => {
    if (partyId === null) return fields.fail("party", DEBT_MESSAGES.partyRequired(direction));
    if (amount === null || !dec(amount).gt(0)) return fields.fail("amount", DEBT_MESSAGES.amountPositive);
    if (method === null) return fields.fail("method", DEBT_MESSAGES.fundRequired);
    const allocations = rows.flatMap((r) => (r.amount !== null && dec(r.amount).gt(0) ? [{ obligationId: r.doc.id, amount: r.amount, suggested: !r.edited }] : []));
    fields.reset();
    lines.sending(allocations.map((a) => a.obligationId));
    void pay.run({ direction, partyId, amount, paymentMethodId: method, note, allocations });
  };

  const columns: Column<AllocRow>[] = [
    { key: "doc", header: "Chứng từ", cell: (r) => <DocNo value={r.doc.source_doc_no} /> },
    { key: "due", header: "Hạn", cell: (r) => fd(r.doc.next_due) },
    { key: "open", header: "Còn nợ", numeric: true, cell: (r) => money(r.doc.open) },
    {
      key: "alloc",
      header: "Phân bổ",
      numeric: true,
      cell: (r) => (
        <MoneyInput
          aria-label={`Phân bổ ${r.doc.source_doc_no}`}
          size="sm"
          value={r.amount}
          invalid={check.overOpen.includes(r.doc.id) || lines.errors.has(r.doc.id)}
          onChange={(v) => {
            setEdits((cur) => new Map(cur).set(r.doc.id, v));
            lines.clear(r.doc.id);
          }}
        />
      ),
    },
  ];

  const fixedName = fixedParty !== null ? (ledger.data?.party.name ?? "") : null;
  const refusedLines = rows.filter((r) => lines.errors.has(r.doc.id));

  return (
    <CommitDialog
      open
      wide
      onClose={onClose}
      title={side.payTitle}
      sub="Gợi ý phân bổ theo hạn cũ nhất; bạn xem và sửa trước khi ghi. Phần chưa phân bổ thành tiền ứng trước."
      submitLabel={side.submit}
      onSubmit={submit}
      submitDisabled={blocked}
      pending={pay.pending}
      live={
        <>
          <ImpactSummary
            items={[
              { label: side.fundImpact, value: money(amount ?? "0") },
              { label: "Phân bổ vào chứng từ", value: money(check.allocated) },
              { label: "Thành tiền ứng trước", value: money(check.advance) },
              { label: "Doanh thu / chi phí", value: "Không phát sinh" },
            ]}
          />
          {check.overPayment ? (
            <Callout tone="bad" role="alert">
              {DEBT_MESSAGES.overPayment}
            </Callout>
          ) : null}
          {check.overOpen.length > 0 ? (
            <Callout tone="bad" role="alert">
              {DEBT_MESSAGES.overOpen}
            </Callout>
          ) : null}
          {refusedLines.length > 0 ? (
            <Callout tone="bad" role="alert">
              {refusedLines.map((r) => `${r.doc.source_doc_no}: ${lines.errors.get(r.doc.id) ?? ""}`).join(" ")}
            </Callout>
          ) : null}
        </>
      }
    >
      <FormGrid>
        <Field label={side.party} error={errors.party}>
          {fixedName !== null ? (
            <Input value={fixedName} readOnly invalid={errors.party !== undefined} />
          ) : (
            <Combobox
              kind="party"
              source={direction === "receivable" ? customerSource : activeSupplierSource}
              value={pickedParty?.id ?? null}
              valueLabel={pickedParty?.name ?? ""}
              aria-label={side.party}
              invalid={errors.party !== undefined}
              onChange={pickParty}
            />
          )}
        </Field>
        <Field label="Số tiền" error={errors.amount}>
          <MoneyInput
            value={amount}
            onChange={(value) => {
              setTyped({ value });
              fields.clear("amount");
            }}
            invalid={errors.amount !== undefined}
            autoFocus
          />
        </Field>
        <Field label={side.fund} error={errors.method}>
          <select
            className="in"
            value={method ?? ""}
            aria-invalid={errors.method !== undefined || undefined}
            onChange={(e) => {
              setPickedMethod(e.target.value);
              fields.clear("method");
            }}
          >
            {(methods.data ?? []).map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Nội dung" error={errors.note}>
          <Input
            value={note}
            maxLength={1000}
            invalid={errors.note !== undefined}
            onChange={(e) => {
              setNote(e.target.value);
              fields.clear("note");
            }}
          />
        </Field>
      </FormGrid>
      {partyId !== null ? (
        <DataTable
          caption="Phân bổ vào chứng từ"
          columns={columns}
          rows={rows}
          rowKey={(r) => r.doc.id}
          flat
          empty="Không có chứng từ còn mở. Toàn bộ số tiền sẽ thành ứng trước."
        />
      ) : null}
    </CommitDialog>
  );
}
