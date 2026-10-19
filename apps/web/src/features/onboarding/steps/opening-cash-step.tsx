import { useId, useRef, useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { fd, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { TableCaption } from "../../../shared/ui/data-table/table-caption";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { ScreenError } from "../../../app/route-fallbacks";
import { useSaveFundOpening, useStepCommand } from "../api/mutations";
import { useOpeningFunds } from "../api/queries";
import { OpeningGate } from "../components/opening-gate";
import { StepCard } from "../components/step-card";
import type { FundAccount } from "../schemas";
import type { ChecklistStep } from "../steps";

const CASH_LABELS = {
  fund: "Quỹ",
  current: "Số dư đầu kỳ đã lưu",
  amount: "Số dư đầu kỳ",
  save: "Lưu số dư",
} as const;

const FUND_KIND_LABELS: Readonly<Record<FundAccount["kind"], string>> = { cash: "Tiền mặt", bank: "Ngân hàng" };

/** Opening cash: the balance of every fund at the end of the cutoff day. */
export function OpeningCashStep({ step, cutoff, confirmed, onOpenProfile }: { step: ChecklistStep; cutoff: string | null; confirmed: boolean; onOpenProfile: () => void }) {
  return (
    <StepCard step={step} note={cutoff ? `Tiền trong từng quỹ cuối ngày ${fd(cutoff)}` : undefined} markDone={!confirmed}>
      <OpeningGate cutoff={cutoff} confirmed={confirmed} onOpenProfile={onOpenProfile}>
        {(date) => <FundTable cutoff={date} />}
      </OpeningGate>
    </StepCard>
  );
}

function FundTable({ cutoff }: { cutoff: string }) {
  const funds = useOpeningFunds();
  const complete = useStepCommand("opening_cash", "complete", { quiet: true });
  const onSaved = () => void complete.run();
  if (funds.isPending) return <ListLoading />;
  if (funds.isError) return <ScreenError error={funds.error} onRetry={() => void funds.refetch()} />;
  return (
    <div className="tw flat">
      <table className="t">
        <TableCaption>Số dư quỹ đầu kỳ</TableCaption>
        <thead>
          <tr>
            <th scope="col">{CASH_LABELS.fund}</th>
            <th scope="col" className="num">
              {CASH_LABELS.current}
            </th>
            <th scope="col" className="num">
              {CASH_LABELS.amount}
            </th>
            <th scope="col">
              <span className="sr-only">Thao tác</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {funds.data.map((f) => (
            <FundRow key={`${f.id}-${f.version}`} fund={f} cutoff={cutoff} onSaved={onSaved} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function FundRow({ fund, cutoff, onSaved }: { fund: FundAccount; cutoff: string; onSaved: () => void }) {
  const [amount, setAmount] = useState<Money | null>(fund.opening_balance);
  const [missing, setMissing] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const errorId = useId();
  const save = useSaveFundOpening({ after: onSaved });
  const submit = () => {
    if (amount === null) {
      setMissing(true);
      input.current?.focus();
      return;
    }
    void save.run({ fund, opening_balance: amount, opening_date: cutoff });
  };
  return (
    <tr>
      <td>
        <CellText main={fund.name} sub={FUND_KIND_LABELS[fund.kind]} />
      </td>
      <td className="num tab-n">{vnd(fund.opening_balance)}</td>
      <td className="num">
        <MoneyInput
          ref={input}
          value={amount}
          onChange={(v) => {
            setAmount(v);
            setMissing(false);
          }}
          aria-label={`${CASH_LABELS.amount} ${fund.name}`}
          invalid={missing}
          {...(missing ? { "aria-describedby": errorId } : {})}
        />
        {missing ? (
          <span className="err-text" role="alert" id={errorId}>
            Nhập số dư đầu kỳ của {fund.name}.
          </span>
        ) : null}
      </td>
      <td className="acts">
        <Button size="sm" variant="primary" pending={save.pending} onClick={submit} aria-label={`${CASH_LABELS.save} ${fund.name}`}>
          {CASH_LABELS.save}
        </Button>
      </td>
    </tr>
  );
}
