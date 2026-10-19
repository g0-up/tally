import { useState } from "react";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { monthStart, shiftMonth } from "../../../shared/lib/dates";
import { Combobox, type ComboItem } from "../../../shared/ui/combobox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { DEBT_MESSAGES } from "../api/adapters";
import { useCreateStatement } from "../api/mutations";
import { customerSource } from "../api/queries";
import { useFieldErrors } from "../field-errors";
import type { Statement } from "../schemas";

type StatementField = "party" | "from" | "to";

/** The request fields the dialog shows a refusal under. */
const API_FIELDS: Readonly<Record<string, StatementField>> = { party_id: "party", period_from: "from", period_to: "to" };

export interface NewStatementDialogProps {
  today: string;
  /** The customer the dialog was opened for ("Bảng kê" on a debt row). */
  party: { id: string; name: string } | null;
  onClose: () => void;
  onCreated: (s: Statement) => void;
}

/** Prototype newSTM: a reconciliation statement of one customer over a period. */
export function NewStatementDialog({ today, party: preset, onClose, onCreated }: NewStatementDialogProps) {
  const [party, setParty] = useState<Pick<ComboItem, "id" | "name"> | null>(preset);
  // Until the owner picks dates the period follows the business date, which may still be loading.
  const [pickedFrom, setFrom] = useState<{ value: string | null } | null>(null);
  const [pickedTo, setTo] = useState<{ value: string | null } | null>(null);
  const from = pickedFrom !== null ? pickedFrom.value : monthStart(shiftMonth(today, -1));
  const to = pickedTo !== null ? pickedTo.value : today;
  const fields = useFieldErrors(API_FIELDS);
  const create = useCreateStatement((s) => {
    onClose();
    onCreated(s);
  }, fields.onFieldError);

  const submit = () => {
    if (party === null) return fields.fail("party", DEBT_MESSAGES.partyRequired("receivable"));
    if (from === null) return fields.fail("from", DEBT_MESSAGES.periodInvalid);
    if (to === null || to < from) return fields.fail("to", DEBT_MESSAGES.periodInvalid);
    fields.reset();
    void create.run({ partyId: party.id, direction: "receivable", from, to });
  };

  return (
    <CommitDialog open onClose={onClose} title="Lập bảng kê đối soát" submitLabel="Lập bảng kê" onSubmit={submit} pending={create.pending} {...(create.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}>
      <FormGrid>
        <Field label="Khách hàng" error={fields.errors.party}>
          <Combobox
            kind="party"
            source={customerSource}
            value={party?.id ?? null}
            valueLabel={party?.name ?? ""}
            aria-label="Khách hàng"
            invalid={fields.errors.party !== undefined}
            onChange={(item) => {
              setParty(item);
              fields.clear("party");
            }}
          />
        </Field>
        <Field label="Từ ngày" error={fields.errors.from}>
          <DatePicker
            value={from}
            onChange={(value) => {
              setFrom({ value });
              fields.clear("from");
            }}
            today={today}
            aria-label="Từ ngày"
            invalid={fields.errors.from !== undefined}
          />
        </Field>
        <Field label="Đến ngày" error={fields.errors.to}>
          <DatePicker
            value={to}
            onChange={(value) => {
              setTo({ value });
              fields.clear("to");
            }}
            today={today}
            aria-label="Đến ngày"
            invalid={fields.errors.to !== undefined}
          />
        </Field>
      </FormGrid>
    </CommitDialog>
  );
}
