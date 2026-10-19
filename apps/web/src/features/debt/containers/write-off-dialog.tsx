import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { MoneyInput } from "../../../shared/ui/money-input";
import { DEBT_MESSAGES } from "../api/adapters";
import { useWriteOff } from "../api/mutations";
import { useReasons } from "../api/queries";
import { useFieldErrors } from "../field-errors";
import type { LedgerDocument } from "../schemas";

/** The request fields the dialog shows a refusal under; the document itself is fixed. */
const API_FIELDS = { amount: "amount", reason_id: "reason", target_obligation_id: "amount" } as const satisfies Record<string, "amount" | "reason">;

export interface WriteOffDialogProps {
  doc: LedgerDocument;
  onClose: () => void;
}

/**
 * Prototype writeOff: the owner forgives part or all of an open document. The server asks for the
 * password first, then for the debt_write_off confirmation; useCommit runs both prompts.
 */
export function WriteOffDialog({ doc, onClose }: WriteOffDialogProps) {
  const reasons = useReasons("write_off");
  const [amount, setAmount] = useState<Money | null>(doc.open);
  const [picked, setPicked] = useState<string | null>(null);
  const reasonId = picked ?? reasons.data?.[0]?.id ?? null;
  const fields = useFieldErrors<"amount" | "reason">(API_FIELDS);
  const writeOff = useWriteOff(onClose, fields.onFieldError);
  // What stays open on the document once the amount typed is forgiven; none while it is not valid.
  const left = amount !== null && dec(amount).gt(0) && !dec(amount).gt(doc.open) ? dec(doc.open).minus(amount) : null;

  const submit = () => {
    if (amount === null || !dec(amount).gt(0)) return fields.fail("amount", DEBT_MESSAGES.amountPositive);
    if (dec(amount).gt(doc.open)) return fields.fail("amount", DEBT_MESSAGES.writeOffOverOpen);
    if (reasonId === null) return fields.fail("reason", DEBT_MESSAGES.reasonRequired);
    fields.reset();
    void writeOff.run({
      partyId: doc.party_id,
      direction: doc.direction,
      obligation: { id: doc.id, docNo: doc.source_doc_no, version: doc.version },
      amount,
      reasonId,
      note: "",
    });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Đề nghị giảm/xóa nợ ${doc.source_doc_no}`}
      sub="Giảm/xóa nợ là quyết định của chủ cửa hàng, ghi nhật ký kèm lý do; không phải tiền thu và không làm tăng doanh thu."
      submitLabel="Ghi giảm nợ"
      onSubmit={submit}
      pending={writeOff.pending}
      live={
        <ImpactSummary
          items={[
            { label: "Chứng từ còn nợ", value: vnd(doc.open) },
            { label: "Còn nợ sau khi giảm", value: left !== null ? vnd(left) : "—" },
          ]}
        />
      }
    >
      <FormGrid>
        <Field label="Số tiền" error={fields.errors.amount}>
          <MoneyInput
            value={amount}
            onChange={(v) => {
              setAmount(v);
              fields.clear("amount");
            }}
            invalid={fields.errors.amount !== undefined}
            autoFocus
          />
        </Field>
        <Field label="Lý do" error={fields.errors.reason}>
          <select
            className="in"
            value={reasonId ?? ""}
            aria-invalid={fields.errors.reason !== undefined || undefined}
            onChange={(e) => {
              setPicked(e.target.value);
              fields.clear("reason");
            }}
          >
            {(reasons.data ?? []).map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </Field>
      </FormGrid>
    </CommitDialog>
  );
}
