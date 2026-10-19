import { useState } from "react";
import { useSearchParams } from "react-router";
import { errorMessage } from "../../../shared/api/api-error";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { isIsoDate } from "../../../shared/lib/dates";
import { fd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { PeriodBar } from "../../../shared/ui/period-bar";
import { PERIODS, type Period } from "../../../shared/ui/period";
import { PayDialog, type PayDialogProps } from "../../debt";
import { useDashboard } from "../api/queries";
import { CashBand } from "../components/cash-band";
import { DebtBand } from "../components/debt-band";
import { balanceDate, periodLabel } from "../period-label";

type Paying = Pick<PayDialogProps, "direction" | "partyId">;

const DEFAULT_PERIOD: Period = "month";

const parsePeriod = (v: string | null): Period => PERIODS.find((p) => p === v) ?? DEFAULT_PERIOD;

/**
 * Bảng điều khiển (/overview/dashboard): money in and out of a period and the debts on both
 * ledgers, each beside the previous period. `?period=day|7d|month|year&date=` choose the period;
 * every figure is the API's.
 */
export default function DashboardPage() {
  const [params, setParams] = useSearchParams();
  const period = parsePeriod(params.get("period"));
  const rawDate = params.get("date");
  const date = rawDate !== null && isIsoDate(rawDate) ? rawDate : null;
  const businessDate = useBusinessDate();
  const dash = useDashboard(period, date);
  const [paying, setPaying] = useState<Paying | null>(null);

  // Placeholder data belongs to the previous choice until the new period arrives: it stays on screen
  // dimmed, and goes once the new period fails (the query drops it on error).
  const stale = dash.isPlaceholderData;
  const data = stale ? undefined : dash.data;
  const shown = dash.data;
  const today = shown?.today ?? businessDate;

  const write = (next: { period?: Period; date?: string | null }) =>
    setParams(
      (cur) => {
        const out = new URLSearchParams(cur);
        if (next.period !== undefined) out.set("period", next.period);
        if (next.date === null) out.delete("date");
        else if (next.date !== undefined) out.set("date", next.date);
        return out;
      },
      { replace: true },
    );

  return (
    <>
      <PageHeader
        eyebrow="Tổng quan"
        title="Thu, chi và công nợ"
        lead={
          shown ? (
            <>
              Đang xem <b>{periodLabel(shown.period)}</b>. Thu – chi là tiền thực vào/ra quỹ (không tính số dư đầu kỳ và chuyển giữa các quỹ). Công nợ tính tại ngày{" "}
              {fd(balanceDate(shown.period, shown.today))}.
            </>
          ) : undefined
        }
        actions={
          <>
            <Button variant="primary" onClick={() => setPaying({ direction: "receivable", partyId: null })}>
              Thu tiền khách
            </Button>
            <Button onClick={() => setPaying({ direction: "payable", partyId: null })}>Trả tiền NCC</Button>
          </>
        }
      />
      <PeriodBar
        period={period}
        onPeriodChange={(p) => write({ period: p })}
        date={date ?? today}
        onDateChange={(d) => write({ date: d })}
        onPrev={() => (data ? write({ date: data.previous.date }) : undefined)}
        onNext={() => (data ? write({ date: data.next.date }) : undefined)}
        onToday={() => write({ date: null })}
        today={today}
      />
      {dash.isError ? (
        <Callout tone="bad" role="alert">
          {errorMessage(dash.error)}
        </Callout>
      ) : null}
      {/* Always there and outside the busy block, so the switch to a new period is announced. */}
      <div role="status">
        {shown && stale ? <p className="hint">Đang tải số liệu kỳ đã chọn, số bên dưới là của {periodLabel(shown.period)}.</p> : null}
      </div>
      {shown ? (
        <div aria-busy={stale || undefined} style={stale ? { opacity: 0.55 } : undefined}>
          <CashBand data={shown} />
          <DebtBand data={shown} onPay={(direction, partyId) => setPaying({ direction, partyId })} />
        </div>
      ) : dash.isPending ? (
        <ListLoading />
      ) : null}
      {paying ? <PayDialog direction={paying.direction} partyId={paying.partyId} onClose={() => setPaying(null)} /> : null}
    </>
  );
}
