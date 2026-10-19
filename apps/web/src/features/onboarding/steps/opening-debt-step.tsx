import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Combobox } from "../../../shared/ui/combobox";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Textarea } from "../../../shared/ui/input";
import { KeyValueGrid } from "../../../shared/ui/key-value-grid";
import { LinkButton } from "../../../shared/ui/link-button";
import { MoneyInput } from "../../../shared/ui/money-input";
import { ScreenError } from "../../../app/route-fallbacks";
import { CustomerQuickDialog } from "../../crm";
import { customerSource } from "../../debt";
import { SupplierQuickDialog, activeSupplierSource } from "../../partners";
import { usePostOpeningDebt, type OpeningDebtVars } from "../api/mutations";
import { useActiveReasons, useOpeningDebts } from "../api/queries";
import { CutoffGate, LockedCallout, NoneButton } from "../components/opening-gate";
import { useOnbFail } from "../hooks/use-onb-fail";
import { OnbField, OnbSelect } from "../components/onb-field";
import { withoutError, type FieldErrors } from "../components/field-errors";
import { useOnboardingScreen } from "../onboarding-context";
import type { OpeningDebt } from "../schemas";

type Direction = OpeningDebtVars["direction"];

const NOTE_MAX = 1000;
/** The seeded reason the opening balances use; any active debt adjustment reason is accepted. */
const OPENING_REASON_NAME = "Số dư đầu kỳ";

interface Draft {
  direction: Direction;
  party: { id: string; name: string } | null;
  amount: Money | null;
  reasonId: string | null;
  due: string | null;
  note: string;
}

const emptyDraft = (direction: Direction, reasonId: string | null = null): Draft => ({ direction, party: null, amount: null, reasonId, due: null, note: "" });

/** The API's field names, for the errors of a refused posting. */
const API_FIELDS: Readonly<Record<string, keyof Draft>> = { party_id: "party", amount: "amount", reason_id: "reasonId", due_date: "due", note: "note" };

/** Prototype `OB_BODY.opening_debt`: one balance per customer or supplier as of the cutoff, posted one at a time. */
export function OpeningDebtStep() {
  const { cutoff, confirmed, goTo } = useOnboardingScreen();
  const posted = useOpeningDebts();
  const postedView = posted.isError ? <ScreenError error={posted.error} onRetry={() => void posted.refetch()} /> : posted.data ? <PostedDebts debts={posted.data.debts} more={posted.data.more} /> : null;
  if (confirmed) {
    return (
      <>
        <LockedCallout cutoff={cutoff} />
        {postedView}
      </>
    );
  }
  if (cutoff === null) return <CutoffGate onOpenProfile={() => goTo("store_profile")} />;
  return (
    <>
      <DebtForm cutoff={cutoff} noneShown={posted.data?.debts.length === 0} />
      {postedView}
    </>
  );
}

function DebtForm({ cutoff, noneShown }: { cutoff: string; noneShown: boolean }) {
  const { cta } = useOnboardingScreen();
  const [draft, setDraft] = useState<Draft>(() => emptyDraft("receivable"));
  const [errors, setErrors] = useState<FieldErrors>({});
  const fail = useOnbFail(setErrors);
  const [adding, setAdding] = useState<Direction | null>(null);
  const reasons = useActiveReasons("debt_adjustment");
  const reasonList = reasons.data ?? [];
  const reasonId = draft.reasonId ?? (reasonList.find((r) => r.name === OPENING_REASON_NAME) ?? reasonList[0])?.id ?? null;
  // The API completes the step with the first opening posting.
  const post = usePostOpeningDebt({
    setFieldError: (field, message) => {
      const key = API_FIELDS[field];
      if (key) setErrors((e) => ({ ...e, [key]: message }));
    },
    after: () => {
      setDraft((d) => emptyDraft(d.direction, d.reasonId));
      setErrors({});
    },
  });

  const isAR = draft.direction === "receivable";
  const partyLabel = isAR ? "Khách hàng" : "Nhà cung cấp";
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setErrors((e) => withoutError(e, key));
  };
  // Prototype `A.onbDir`: the other side keeps the amount, due date and note, not the party.
  const turn = (direction: Direction) => {
    if (direction === draft.direction) return;
    setDraft((d) => ({ ...d, direction, party: null }));
    setErrors({});
  };
  const select = (party: { id: string; name: string }) => {
    setAdding(null);
    set("party", { id: party.id, name: party.name });
  };

  const submit = () => {
    const found: Record<string, string> = {};
    if (draft.party === null) found.party = isAR ? "Chọn khách hàng." : "Chọn nhà cung cấp.";
    if (draft.amount === null || !dec(draft.amount).gt(0)) found.amount = "Số tiền nợ phải lớn hơn 0.";
    if (reasonId === null) found.reasonId = "Chưa có lý do điều chỉnh công nợ nào đang dùng.";
    if (Object.keys(found).length > 0) return fail(found);
    if (draft.party === null || draft.amount === null || reasonId === null) return;
    setErrors({});
    void post.run({
      party_id: draft.party.id,
      direction: draft.direction,
      amount: draft.amount,
      reason_id: reasonId,
      opening_cutoff_date: cutoff,
      ...(draft.due !== null ? { due_date: draft.due } : {}),
      ...(draft.note.trim() !== "" ? { note: draft.note.trim() } : {}),
    });
  };

  return (
    <>
      <div className="onb-seg" role="group" aria-label="Chiều công nợ">
        <button type="button" className="chip" aria-pressed={isAR} onClick={() => turn("receivable")}>
          Khách hàng nợ cửa hàng
        </button>
        <button type="button" className="chip" aria-pressed={!isAR} onClick={() => turn("payable")}>
          Cửa hàng nợ nhà cung cấp
        </button>
      </div>
      <div className="fg">
        <OnbField id="ob-debtForm-party" label={partyLabel} required hint={<LinkButton onClick={() => setAdding(draft.direction)}>+ {partyLabel} mới</LinkButton>} error={errors.party}>
          <Combobox
            key={draft.direction}
            id="ob-debtForm-party"
            kind="party"
            source={isAR ? customerSource : activeSupplierSource}
            value={draft.party?.id ?? null}
            valueLabel={draft.party?.name ?? ""}
            placeholder={`— Chọn ${partyLabel.toLowerCase()} —`}
            onChange={(it) => set("party", { id: it.id, name: it.name })}
          />
        </OnbField>
        <OnbField id="ob-debtForm-amount" label="Số tiền nợ (₫)" required error={errors.amount}>
          <MoneyInput id="ob-debtForm-amount" value={draft.amount} onChange={(v) => set("amount", v)} aria-required="true" />
        </OnbField>
        <OnbField id="ob-debtForm-reason" label="Lý do" error={errors.reasonId}>
          <OnbSelect id="ob-debtForm-reason" className="in" value={reasonId ?? ""} onChange={(e) => set("reasonId", e.target.value)}>
            {reasonList.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </OnbSelect>
        </OnbField>
        <OnbField id="ob-debtForm-due" label="Hạn thanh toán" hint="Để trống nếu chưa hẹn." error={errors.due}>
          <DatePicker id="ob-debtForm-due" value={draft.due} onChange={(v) => set("due", v)} />
        </OnbField>
        <OnbField id="ob-debtForm-note" label="Ghi chú" wide error={errors.note}>
          <Textarea id="ob-debtForm-note" rows={2} value={draft.note} maxLength={NOTE_MAX} onChange={(e) => set("note", e.target.value)} />
        </OnbField>
      </div>
      <ButtonRow>
        <Button variant={cta} pending={post.pending} disabled={reasons.isPending} onClick={submit}>
          Ghi công nợ đầu kỳ
        </Button>
        {noneShown ? <NoneButton step="opening_debt" /> : null}
      </ButtonRow>
      {adding === "receivable" ? <CustomerQuickDialog onClose={() => setAdding(null)} onCreated={select} /> : null}
      {adding === "payable" ? <SupplierQuickDialog onClose={() => setAdding(null)} onCreated={select} /> : null}
    </>
  );
}

const DEBT_COLUMNS: ReadonlyArray<Column<OpeningDebt>> = [
  { key: "dir", header: "Chiều", cell: (d) => (d.direction === "receivable" ? <span className="tag">Khách nợ</span> : <span className="tag alt">Nợ NCC</span>) },
  { key: "party", header: "Đối tác", cell: (d) => d.party_name },
  { key: "reason", header: "Lý do", cell: (d) => <CellText main={d.reason_name} sub={d.note} /> },
  { key: "due", header: "Hạn", cell: (d) => fd(d.obligation?.due_date ?? null) },
  { key: "amount", header: "Số tiền", numeric: true, cell: (d) => vnd(d.amount) },
];

/** Prototype "Đã ghi công nợ đầu kỳ": every posted opening balance and the two totals. */
function PostedDebts({ debts, more }: { debts: readonly OpeningDebt[]; more: boolean }) {
  if (debts.length === 0) return null;
  const total = (direction: Direction) => debts.filter((d) => d.direction === direction).reduce((s, d) => s.plus(d.amount), dec(0));
  return (
    <>
      <h3 className="sec-h">
        Đã ghi công nợ đầu kỳ <small className="hint">{debts.length} khoản</small>
      </h3>
      <DataTable caption="Đã ghi công nợ đầu kỳ" columns={DEBT_COLUMNS} rows={debts} rowKey={(d) => d.id} flat />
      {more ? <p className="hint">Chỉ hiện các chứng từ gần nhất.</p> : null}
      <KeyValueGrid
        items={[
          { label: "Khách nợ cửa hàng (phải thu)", value: vnd(total("receivable")) },
          { label: "Cửa hàng nợ NCC (phải trả)", value: vnd(total("payable")) },
        ]}
      />
    </>
  );
}
