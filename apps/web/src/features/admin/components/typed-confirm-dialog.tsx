import { useState, type ReactNode } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";

export interface TypedConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  /** What the action does and to whom. */
  children: ReactNode;
  /** The text the admin types to unlock the action (the store slug, the invoice number). */
  phrase: string;
  /** Name of what is typed, e.g. "địa chỉ cửa hàng" or "số hóa đơn". */
  phraseLabel: string;
  submitLabel: string;
  onConfirm: () => void;
  pending: boolean;
}

/**
 * A destructive admin action: the submit button stays disabled until the admin types the exact
 * slug or number of what it acts on, so a slip on the wrong row cannot suspend or void it.
 */
export function TypedConfirmDialog({ open, onClose, title, children, phrase, phraseLabel, submitLabel, onConfirm, pending }: TypedConfirmDialogProps) {
  const [typed, setTyped] = useState("");
  const matches = typed.trim() === phrase;
  const close = () => {
    setTyped("");
    onClose();
  };
  return (
    <CommitDialog open={open} onClose={close} title={title} onSubmit={onConfirm} submitLabel={submitLabel} submitVariant="danger" submitDisabled={!matches} pending={pending}>
      {children}
      <Field
        label={
          <>
            Gõ {phraseLabel} <b>{phrase}</b> để xác nhận
          </>
        }
      >
        <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
    </CommitDialog>
  );
}
