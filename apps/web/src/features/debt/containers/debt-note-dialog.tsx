import { useState } from "react";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { addDays } from "../../../shared/lib/dates";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { useCollectionNote } from "../api/mutations";
import { useReminderConsent } from "../api/queries";
import { useFieldErrors } from "../field-errors";

export interface DebtNoteDialogProps {
  partyId: string;
  onClose: () => void;
}

/** The request fields the dialog shows a refusal under. */
const API_FIELDS = { body: "body", promised_date: "promised" } as const satisfies Record<string, "body" | "promised">;

/** Text saved when the owner leaves "Nội dung" empty (prototype debtNote). */
const DEFAULT_NOTE = "Đã gọi nhắc nợ";

/** Prototype debtNote: what was said to the customer and the date they promised to pay. */
export function DebtNoteDialog({ partyId, onClose }: DebtNoteDialogProps) {
  const today = useBusinessDate();
  const consent = useReminderConsent(partyId);
  const [body, setBody] = useState("");
  const [promise, setPromise] = useState<string | null>(null);
  const promised = promise ?? addDays(today, 7);
  const [cleared, setCleared] = useState(false);
  const fields = useFieldErrors<"body" | "promised">(API_FIELDS);
  const note = useCollectionNote(onClose, fields.onFieldError);

  const submit = () => {
    fields.reset();
    void note.run({ partyId, direction: "receivable", body: body.trim() || DEFAULT_NOTE, promisedDate: cleared ? null : promised });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Ghi chú nhắc nợ"
      sub={consent.data === false ? "Khách chưa đồng ý nhận tin nhắn. Chỉ ghi chú liên hệ trực tiếp." : "Ghi lại nội dung liên hệ và ngày khách hẹn thanh toán."}
      submitLabel="Lưu ghi chú"
      onSubmit={submit}
      pending={note.pending}
      {...(note.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
    >
      <FormGrid>
        <Field label="Nội dung" error={fields.errors.body}>
          <Input
            value={body}
            maxLength={1000}
            invalid={fields.errors.body !== undefined}
            onChange={(e) => {
              setBody(e.target.value);
              fields.clear("body");
            }}
            autoFocus
          />
        </Field>
        <Field label="Khách hẹn trả ngày" error={fields.errors.promised}>
          <DatePicker
            value={cleared ? null : promised}
            today={today}
            aria-label="Khách hẹn trả ngày"
            invalid={fields.errors.promised !== undefined}
            onChange={(v) => {
              fields.clear("promised");
              setCleared(v === null);
              if (v !== null) setPromise(v);
            }}
          />
        </Field>
      </FormGrid>
    </CommitDialog>
  );
}
