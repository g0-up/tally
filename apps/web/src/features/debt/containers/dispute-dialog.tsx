import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { DEBT_MESSAGES } from "../api/adapters";
import { useDisputeStatement } from "../api/mutations";
import { useFieldErrors } from "../field-errors";
import type { Statement } from "../schemas";

export interface DisputeDialogProps {
  statement: Statement;
  onClose: () => void;
}

const API_FIELDS = { note: "note" } as const;

/** Prototype stmDispute: what the customer contests on a sent statement. */
export function DisputeDialog({ statement, onClose }: DisputeDialogProps) {
  const [note, setNote] = useState("");
  const fields = useFieldErrors<"note">(API_FIELDS);
  const dispute = useDisputeStatement(onClose, fields.onFieldError);
  const submit = () => {
    if (note.trim() === "") return fields.fail("note", DEBT_MESSAGES.disputeRequired);
    fields.reset();
    void dispute.run({ statement, note });
  };
  return (
    <CommitDialog open onClose={onClose} title={`Ghi nhận tranh chấp ${statement.doc_no}`} submitLabel="Ghi nhận" onSubmit={submit} pending={dispute.pending}>
      <Field label="Nội dung khách phản hồi" error={fields.errors.note}>
        <Input
          value={note}
          maxLength={1000}
          invalid={fields.errors.note !== undefined}
          onChange={(e) => {
            setNote(e.target.value);
            fields.clear("note");
          }}
          autoFocus
        />
      </Field>
    </CommitDialog>
  );
}
