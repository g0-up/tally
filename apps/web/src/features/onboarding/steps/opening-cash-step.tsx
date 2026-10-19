import { useState } from "react";
import { ZERO_MONEY, type Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { ScreenError } from "../../../app/route-fallbacks";
import { useSaveFundOpenings, useStepCommand } from "../api/mutations";
import { useOpeningFunds } from "../api/queries";
import { CutoffGate, LockedCallout } from "../components/opening-gate";
import { useOnbFail } from "../hooks/use-onb-fail";
import { OnbErr } from "../components/onb-field";
import { errRef, withoutError, type FieldErrors } from "../components/field-errors";
import { useOnboardingScreen } from "../onboarding-context";
import type { FundAccount } from "../schemas";

const FUND_KIND_LABELS: Readonly<Record<FundAccount["kind"], string>> = { cash: "Tiền mặt", bank: "Ngân hàng" };

const ctlId = (i: number) => `ob-funds-${i}-val`;
const sumOf = (amounts: ReadonlyArray<Money | null>) => amounts.reduce((s, v) => s.plus(v ?? 0), dec(0));

const LOCKED_COLUMNS: ReadonlyArray<Column<FundAccount>> = [
  { key: "fund", header: "Quỹ", cell: (f) => <CellText main={f.name} sub={FUND_KIND_LABELS[f.kind]} />, footer: "Tổng tiền các quỹ" },
  { key: "balance", header: "Số dư đầu kỳ", numeric: true, cell: (f) => vnd(f.opening_balance) },
];

/** Prototype `OB_BODY.opening_cash`: the balance of every fund at the end of the cutoff day, saved together. */
export function OpeningCashStep() {
  const { cutoff, confirmed, goTo } = useOnboardingScreen();
  const funds = useOpeningFunds();
  if (!confirmed && cutoff === null) return <CutoffGate onOpenProfile={() => goTo("store_profile")} />;
  if (funds.isPending) return <ListLoading />;
  if (funds.isError) return <ScreenError error={funds.error} onRetry={() => void funds.refetch()} />;
  if (confirmed || cutoff === null) {
    const columns = LOCKED_COLUMNS.map((c) => (c.key === "balance" ? { ...c, footer: vnd(sumOf(funds.data.map((f) => f.opening_balance))) } : c));
    return (
      <>
        <LockedCallout cutoff={cutoff} />
        <DataTable caption="Số dư quỹ đầu kỳ" columns={columns} rows={funds.data} rowKey={(f) => f.id} flat />
      </>
    );
  }
  return <FundForm funds={funds.data} cutoff={cutoff} />;
}

function FundForm({ funds, cutoff }: { funds: readonly FundAccount[]; cutoff: string }) {
  const { cta, goTo } = useOnboardingScreen();
  // A fund already saved starts from its balance, so saving again does not reset it to 0.
  const [values, setValues] = useState<Readonly<Record<string, Money | null>>>(() => Object.fromEntries(funds.map((f) => [f.id, f.opening_date ? f.opening_balance : null])));
  const [errors, setErrors] = useState<FieldErrors>({});
  const fail = useOnbFail(setErrors);
  const complete = useStepCommand("opening_cash", "complete", { quiet: true, after: () => goTo("confirm_opening_balances") });
  const save = useSaveFundOpenings({ after: () => void complete.run() });
  const valueOf = (f: FundAccount) => values[f.id] ?? null;

  const submit = () => {
    const found: Record<string, string> = {};
    funds.forEach((f, i) => {
      const v = valueOf(f);
      if (v !== null && dec(v).lt(0)) found[ctlId(i)] = "Nhập số tiền từ 0 trở lên.";
    });
    if (Object.keys(found).length > 0) return fail(found);
    setErrors({});
    void save.run({ entries: funds.map((f) => ({ fund: f, opening_balance: valueOf(f) ?? ZERO_MONEY })), opening_date: cutoff });
  };

  return (
    <>
      <div className="tw flat">
        <table className="t onb-tbl onb-stack">
          <TableCaption>Số dư quỹ đầu kỳ</TableCaption>
          <thead>
            <tr>
              <th scope="col">Quỹ</th>
              <th scope="col" className="num">
                Đã lưu
              </th>
              <th scope="col" className="num">
                Số dư đầu kỳ (₫)
              </th>
            </tr>
          </thead>
          <tbody>
            {funds.map((f, i) => {
              const id = ctlId(i);
              const describedBy = errRef(id, errors[id]);
              return (
                <tr key={f.id}>
                  <td>
                    <label htmlFor={id}>
                      <b style={{ color: "var(--navy-900)", fontWeight: 600 }}>{f.name}</b>
                    </label>
                    <span className="sub">{FUND_KIND_LABELS[f.kind]}</span>
                  </td>
                  <td className="num tab-n" data-l="Đã lưu">
                    {f.opening_date ? vnd(f.opening_balance) : <span className="hint">Chưa lưu</span>}
                  </td>
                  <td data-l="Số dư đầu kỳ (₫)">
                    <MoneyInput
                      id={id}
                      className="num"
                      placeholder="0"
                      value={valueOf(f)}
                      onChange={(v) => {
                        setValues((s) => ({ ...s, [f.id]: v }));
                        setErrors((e) => withoutError(e, id));
                      }}
                      invalid={describedBy !== undefined}
                      {...(describedBy ? { "aria-describedby": describedBy } : {})}
                    />
                    <OnbErr id={id} error={errors[id]} />
                  </td>
                </tr>
              );
            })}
          </tbody>
          <tfoot>
            <tr>
              <td colSpan={2}>Tổng tiền các quỹ</td>
              <td className="num tab-n">{vnd(sumOf(funds.map(valueOf)))}</td>
            </tr>
          </tfoot>
        </table>
      </div>
      <ButtonRow>
        <Button variant={cta} pending={save.pending || complete.pending} onClick={submit}>
          Lưu số dư các quỹ
        </Button>
        <span className="hint">Lưu tất cả quỹ một lần. Ô để trống được tính là 0 ₫.</span>
      </ButtonRow>
    </>
  );
}
