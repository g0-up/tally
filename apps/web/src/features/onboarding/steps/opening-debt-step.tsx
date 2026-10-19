import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { dec } from "../../../shared/lib/decimal";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Combobox } from "../../../shared/ui/combobox";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Textarea } from "../../../shared/ui/input";
import { LinkButton } from "../../../shared/ui/link-button";
import { MoneyInput } from "../../../shared/ui/money-input";
import { toast } from "../../../shared/ui/toast-store";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { CustomerQuickDialog } from "../../crm";
import { customerSource } from "../../debt";
import { SupplierQuickDialog, activeSupplierSource } from "../../partners";
import { usePostOpeningDebt, useStepCommand, type OpeningDebtVars } from "../api/mutations";
import { useActiveReasons } from "../api/queries";
import { OpeningGate } from "../components/opening-gate";
import { StepCard } from "../components/step-card";
import type { ChecklistStep } from "../steps";

type Direction = OpeningDebtVars["direction"];

const DEBT_LABELS = {
  direction: "Loại công nợ",
  receivable: "Khách hàng nợ cửa hàng",
  payable: "Cửa hàng nợ nhà cung cấp",
  customer: "Khách hàng",
  supplier: "Nhà cung cấp",
  newCustomer: "+ Khách hàng mới",
  newSupplier: "+ Nhà cung cấp mới",
  amount: "Số tiền nợ",
  reason: "Lý do",
  dueDate: "Hạn thanh toán",
  note: "Ghi chú",
  post: "Ghi công nợ đầu kỳ",
} as const;

const NOTE_MAX = 1000;
/** The seeded reason the opening balances use; any active debt adjustment reason is accepted. */
const OPENING_REASON_NAME = "Số dư đầu kỳ";

interface Draft {
  direction: Direction;
  party: { id: string; name: string } | null;
  amount: Money | null;
  reasonId: string | null;
  dueDate: string | null;
  note: string;
}

const emptyDraft = (direction: Direction): Draft => ({ direction, party: null, amount: null, reasonId: null, dueDate: null, note: "" });

/** Opening debt: one balance per customer or supplier as of the cutoff, posted one at a time. */
export function OpeningDebtStep({ step, cutoff, confirmed, onOpenProfile }: { step: ChecklistStep; cutoff: string | null; confirmed: boolean; onOpenProfile: () => void }) {
  return (
    <StepCard step={step} note={cutoff ? `Số còn nợ cuối ngày ${fd(cutoff)}` : undefined} markDone={!confirmed}>
      <OpeningGate cutoff={cutoff} confirmed={confirmed} onOpenProfile={onOpenProfile}>
        {(date) => <DebtForm cutoff={date} />}
      </OpeningGate>
    </StepCard>
  );
}

function DebtForm({ cutoff }: { cutoff: string }) {
  const [draft, setDraft] = useState<Draft>(() => emptyDraft("receivable"));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const focusFirstInvalid = useFocusFirstInvalid();
  const [adding, setAdding] = useState<Direction | null>(null);
  const reasons = useActiveReasons("debt_adjustment");
  const reasonList = reasons.data ?? [];
  const reasonId = draft.reasonId ?? (reasonList.find((r) => r.name === OPENING_REASON_NAME) ?? reasonList[0])?.id ?? null;
  const complete = useStepCommand("opening_debt", "complete", { quiet: true });
  const post = usePostOpeningDebt({
    setFieldError: (field, message) => setErrors((e) => ({ ...e, [field]: message })),
    after: () => {
      setDraft((d) => ({ ...emptyDraft(d.direction), reasonId: d.reasonId }));
      void complete.run();
    },
  });

  const set = (patch: Partial<Draft>) => {
    setDraft((d) => ({ ...d, ...patch }));
    setErrors({});
  };
  const select = (party: { id: string; name: string }) => {
    setAdding(null);
    set({ party: { id: party.id, name: party.name } });
  };
  const partyLabel = draft.direction === "receivable" ? DEBT_LABELS.customer : DEBT_LABELS.supplier;

  const submit = () => {
    const found: Record<string, string> = {};
    if (draft.party === null) found.party_id = `Chọn ${partyLabel.toLowerCase()}.`;
    if (draft.amount === null || !dec(draft.amount).gt(0)) found.amount = "Số tiền nợ phải lớn hơn 0.";
    if (reasonId === null) found.reason_id = "Chưa có lý do điều chỉnh công nợ nào đang dùng.";
    if (draft.note.trim().length > NOTE_MAX) found.note = `Ghi chú tối đa ${NOTE_MAX} ký tự.`;
    setErrors(found);
    const first = Object.values(found)[0];
    if (first !== undefined) {
      toast.commitError(first);
      focusFirstInvalid();
      return;
    }
    if (draft.party === null || draft.amount === null || reasonId === null) return;
    void post.run({
      party_id: draft.party.id,
      direction: draft.direction,
      amount: draft.amount,
      reason_id: reasonId,
      opening_cutoff_date: cutoff,
      ...(draft.dueDate !== null ? { due_date: draft.dueDate } : {}),
      ...(draft.note.trim() !== "" ? { note: draft.note.trim() } : {}),
    });
  };

  return (
    <>
      <ToggleGroup
        label={DEBT_LABELS.direction}
        options={[
          { value: "receivable", label: DEBT_LABELS.receivable },
          { value: "payable", label: DEBT_LABELS.payable },
        ]}
        value={draft.direction}
        onChange={(direction) => set({ direction, party: null })}
      />
      <FormGrid>
        <Field label={partyLabel} error={errors.party_id}>
          <Combobox
            key={draft.direction}
            kind="party"
            source={draft.direction === "receivable" ? customerSource : activeSupplierSource}
            value={draft.party?.id ?? null}
            valueLabel={draft.party?.name ?? ""}
            aria-label={partyLabel}
            invalid={errors.party_id !== undefined}
            onChange={(it) => set({ party: { id: it.id, name: it.name } })}
          />
        </Field>
        <Field label={DEBT_LABELS.amount} error={errors.amount}>
          <MoneyInput value={draft.amount} onChange={(amount) => set({ amount })} invalid={errors.amount !== undefined} />
        </Field>
        <Field label={DEBT_LABELS.reason} error={errors.reason_id}>
          <select className="in" value={reasonId ?? ""} aria-invalid={errors.reason_id !== undefined || undefined} onChange={(e) => set({ reasonId: e.target.value })}>
            {reasonList.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label={DEBT_LABELS.dueDate} hint="Để trống nếu chưa hẹn" error={errors.due_date}>
          <DatePicker value={draft.dueDate} onChange={(dueDate) => set({ dueDate })} aria-label={DEBT_LABELS.dueDate} invalid={errors.due_date !== undefined} />
        </Field>
        <Field label={DEBT_LABELS.note} error={errors.note}>
          <Textarea value={draft.note} maxLength={NOTE_MAX} onChange={(e) => set({ note: e.target.value })} />
        </Field>
      </FormGrid>
      <ButtonRow>
        <LinkButton onClick={() => setAdding(draft.direction)}>{draft.direction === "receivable" ? DEBT_LABELS.newCustomer : DEBT_LABELS.newSupplier}</LinkButton>
        <Button variant="primary" pending={post.pending} onClick={submit}>
          {DEBT_LABELS.post}
        </Button>
      </ButtonRow>
      {adding === "receivable" ? <CustomerQuickDialog onClose={() => setAdding(null)} onCreated={select} /> : null}
      {adding === "payable" ? <SupplierQuickDialog onClose={() => setAdding(null)} onCreated={select} /> : null}
    </>
  );
}
