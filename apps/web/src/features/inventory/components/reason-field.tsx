import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import type { Reason } from "../schemas";
import type { ReasonValue } from "../view-model";

export interface ReasonFieldProps {
  label: string;
  /** Active reasons of the kinds the posting accepts. */
  reasons: readonly Reason[];
  value: ReasonValue;
  onChange: (value: ReasonValue) => void;
  error?: string;
  autoFocus?: boolean;
}

/** Prototype "Lý do" field: the reason catalog when it has entries for this posting, free text otherwise. */
export function ReasonField({ label, reasons, value, onChange, error, autoFocus = false }: ReasonFieldProps) {
  return (
    <Field label={label} error={error}>
      {reasons.length > 0 ? (
        <select className="in" value={value.reasonId} onChange={(e) => onChange({ ...value, reasonId: e.target.value })} aria-invalid={error ? true : undefined} autoFocus={autoFocus}>
          <option value="">Chọn lý do</option>
          {reasons.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      ) : (
        <Input value={value.text} onChange={(e) => onChange({ ...value, text: e.target.value })} maxLength={500} invalid={error !== undefined} autoFocus={autoFocus} />
      )}
    </Field>
  );
}
