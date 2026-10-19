import { useState } from "react";
import { RequireRecentAuth } from "../../../app/guards/require-recent-auth";
import { useApiClient } from "../../../shared/api/api-context";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DatePicker } from "../../../shared/ui/date-picker";
import { Field } from "../../../shared/ui/field";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { Textarea } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import {
  periodLockEventSchema,
  REOPEN_REASON_MAX,
  useOwner,
  usePeriodLocks,
  type PeriodLockEvent,
  type PeriodLocks,
  type ReopenBody,
} from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { PERIOD_LOCK_ACTION } from "../labels";
import { LoadError } from "./load-error";
import { useReadOnlyHint } from "./read-only-hint";

export const REOPEN_LABEL = "Mở lại kỳ";
export const REOPEN_EXCEPTION_TITLE = "Xác nhận mở lại kỳ đã khóa";
export const REOPEN_EXCEPTION_CHECKBOX = "Tôi xác nhận mở lại kỳ";
export const LOCK_LABEL = "Khóa sổ";
export const LOCK_CONFIRM_TITLE = "Xác nhận khóa sổ";
const LOCK_EFFECT = "Chứng từ có ngày từ đó trở về trước không ghi, sửa hay đảo được nữa.";
const LOCK_DATE_REQUIRED = "Chọn ngày khóa sổ.";
const REASON_REQUIRED = "Nhập lý do mở lại kỳ.";

function lockedText(lockedThrough: string | null): string {
  return lockedThrough ? `Đang khóa đến hết ngày ${fd(lockedThrough)}.` : "Chưa khóa kỳ nào.";
}

/** Picks the date, then asks before locking: a lock is undone only with the password and a reason. */
function LockForm({ lockedThrough }: { lockedThrough: string | null }) {
  const client = useApiClient();
  const readOnly = useReadOnlyHint();
  const today = useBusinessDate();
  const [date, setDate] = useState<string | null>(null);
  const [error, setError] = useState<string | undefined>();
  // The date the confirmation is open for.
  const [confirming, setConfirming] = useState<string | null>(null);

  const lock = useCommit({
    mutationFn: (through: string) => client.post("/period-locks", { locked_through: through }, { schema: periodLockEventSchema }),
    success: (ev) => `Đã khóa sổ đến hết ngày ${fd(ev.locked_through)}.`,
    invalidate: [settingsKeys.periodLocks(), platformKeys.onboarding()],
    after: () => {
      setConfirming(null);
      setDate(null);
    },
    setFieldError: (field, message) => {
      if (field !== "locked_through") return;
      // The refusal belongs next to the date, where it can be corrected.
      setConfirming(null);
      setError(message);
    },
  });

  const ask = () => {
    if (!date) {
      setError(LOCK_DATE_REQUIRED);
      return;
    }
    setError(undefined);
    lock.resetKey();
    setConfirming(date);
  };

  return (
    <>
      <div style={{ display: "flex", gap: 8, alignItems: "flex-end", flexWrap: "wrap" }}>
        <Field label="Khóa sổ đến hết ngày" hint={LOCK_EFFECT} error={error}>
          <DatePicker aria-label="Khóa sổ đến hết ngày" value={date} onChange={setDate} invalid={error !== undefined} today={today} />
        </Field>
        <Button variant="navy" disabled={readOnly.readOnly} aria-describedby={readOnly.describedBy} onClick={ask}>
          {LOCK_LABEL}
        </Button>
      </div>
      {readOnly.hint}
      <CommitDialog
        open={confirming !== null}
        onClose={() => setConfirming(null)}
        title={LOCK_CONFIRM_TITLE}
        sub={LOCK_EFFECT}
        onSubmit={() => (confirming ? void lock.run(confirming) : undefined)}
        submitLabel={LOCK_LABEL}
        submitVariant="navy"
        pending={lock.pending}
      >
        <ImpactSummary
          items={[
            { label: "Đang khóa đến", value: lockedThrough ? `Hết ngày ${fd(lockedThrough)}` : "Chưa khóa kỳ nào" },
            { label: "Khóa sổ đến", value: confirming ? `Hết ngày ${fd(confirming)}` : "—" },
            { label: "Mở lại sau này", value: "Cần xác nhận lại mật khẩu và nhập lý do" },
          ]}
        />
      </CommitDialog>
    </>
  );
}

function ReopenControl({ locks, tenantId }: { locks: PeriodLocks; tenantId: string }) {
  const client = useApiClient();
  const today = useBusinessDate();
  const [open, setOpen] = useState(false);
  const [date, setDate] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [errors, setErrors] = useState<{ date?: string; reason?: string }>({});

  const reopen = useCommit({
    mutationFn: (body: ReopenBody, { exceptionConfirmationId }) =>
      client.post(
        "/period-locks/reopen",
        exceptionConfirmationId ? { ...body, exception_confirmation_id: exceptionConfirmationId } : body,
        { schema: periodLockEventSchema },
      ),
    success: (ev) => (ev.locked_through ? `Đã mở lại kỳ. Sổ còn khóa đến hết ngày ${fd(ev.locked_through)}.` : "Đã mở lại toàn bộ kỳ đã khóa."),
    invalidate: [settingsKeys.periodLocks(), platformKeys.onboarding()],
    after: () => setOpen(false),
    setFieldError: (field, message) => {
      if (field === "locked_through") setErrors((cur) => ({ ...cur, date: message }));
      if (field === "reason") setErrors((cur) => ({ ...cur, reason: message }));
    },
    exception: (e, body) => ({
      kind: e.exceptionKind,
      subject: { doc_type: "period_lock", doc_id: tenantId, doc_version: locks.version },
      payload: body,
      title: REOPEN_EXCEPTION_TITLE,
      sub: "Mở lại kỳ cho phép ghi, sửa và đảo chứng từ trong kỳ đã khóa. Lựa chọn được ghi vào nhật ký.",
      checkboxLabel: REOPEN_EXCEPTION_CHECKBOX,
      submitLabel: REOPEN_LABEL,
    }),
  });

  const start = () => {
    reopen.resetKey();
    setDate(null);
    setReason("");
    setErrors({});
    setOpen(true);
  };

  const submit = () => {
    const text = reason.trim();
    if (text === "") {
      setErrors({ reason: REASON_REQUIRED });
      return;
    }
    setErrors({});
    void reopen.run({ locked_through: date, reason: text });
  };

  return (
    <>
      <Button variant="danger" size="sm" onClick={start}>
        {REOPEN_LABEL}
      </Button>
      <CommitDialog
        open={open}
        onClose={() => setOpen(false)}
        title={REOPEN_LABEL}
        sub={lockedText(locks.locked_through)}
        onSubmit={submit}
        submitLabel={REOPEN_LABEL}
        submitVariant="danger"
        pending={reopen.pending}
      >
        <Field label="Khóa lại đến ngày" hint="Để trống để bỏ khóa hoàn toàn." error={errors.date}>
          <DatePicker aria-label="Khóa lại đến ngày" value={date} onChange={setDate} invalid={errors.date !== undefined} today={today} />
        </Field>
        <Field label="Lý do" error={errors.reason}>
          <Textarea rows={3} maxLength={REOPEN_REASON_MAX} value={reason} onChange={(e) => setReason(e.target.value)} invalid={errors.reason !== undefined} />
        </Field>
      </CommitDialog>
    </>
  );
}

const HISTORY_COLUMNS: ReadonlyArray<Column<PeriodLockEvent>> = [
  { key: "at", header: "Thời điểm", cell: (ev) => fdt(ev.created_at) },
  { key: "action", header: "Thao tác", cell: (ev) => PERIOD_LOCK_ACTION[ev.action] },
  { key: "through", header: "Đến ngày", cell: (ev) => (ev.locked_through ? fd(ev.locked_through) : "Bỏ khóa") },
  { key: "reason", header: "Lý do", cell: (ev) => <CellText main={ev.reason ?? "—"} /> },
];

/** "Khóa sổ kỳ": the lock date moves forward freely; moving it back needs the password and a confirmation. */
export function PeriodLockCard() {
  const locks = usePeriodLocks();
  const owner = useOwner();
  return (
    <Card title="Khóa sổ kỳ">
      {locks.isPending ? (
        <ListLoading />
      ) : locks.error ? (
        <LoadError what="kỳ khóa sổ" error={locks.error} />
      ) : (
        <>
          <p>
            <b>{lockedText(locks.data.locked_through)}</b>
          </p>
          <LockForm lockedThrough={locks.data.locked_through} />
          {locks.data.locked_through && owner.data ? (
            <div style={{ marginTop: 12 }}>
              <RequireRecentAuth text="Mở lại kỳ đã khóa cần xác nhận lại mật khẩu.">
                <ButtonRow>
                  <ReopenControl locks={locks.data} tenantId={owner.data.tenant.id} />
                </ButtonRow>
              </RequireRecentAuth>
            </div>
          ) : null}
          <h3 className="sec-h" style={{ margin: "18px 0 8px" }}>
            Lịch sử khóa sổ
          </h3>
          <DataTable caption="Lịch sử khóa sổ" columns={HISTORY_COLUMNS} rows={locks.data.data} rowKey={(ev) => ev.id} flat empty="Chưa khóa sổ lần nào." />
        </>
      )}
    </Card>
  );
}
