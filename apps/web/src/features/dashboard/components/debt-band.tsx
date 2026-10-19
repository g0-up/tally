import { dec } from "../../../shared/lib/decimal";
import { fd, money, vnd } from "../../../shared/lib/format";
import { Band } from "../../../shared/ui/band";
import { Button } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Grid } from "../../../shared/ui/grid";
import { KpiCard } from "../../../shared/ui/kpi-card";
import { TextLink } from "../../../shared/ui/link-button";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { PayDialogProps } from "../../debt";
import { balanceDate, periodLabel } from "../period-label";
import type { Dashboard, DashboardDebtSide } from "../schemas";

type TopParty = DashboardDebtSide["top"][number];
type DebtDirection = PayDialogProps["direction"];

const LEDGER_PATH: Readonly<Record<DebtDirection, string>> = { receivable: "/debts/receivables", payable: "/debts/payables" };
const SCHEDULE_PATH = "/debts/schedule";

const payLabel = (direction: DebtDirection) => (direction === "receivable" ? "Thu tiền" : "Trả tiền");

const badText = (text: string) => <b style={{ color: "var(--bad-ink)" }}>{text}</b>;

export interface DebtBandProps {
  data: Dashboard;
  /** Opens the payment of one party ("Thu tiền" / "Trả tiền"). */
  onPay: (direction: DebtDirection, partyId: string) => void;
}

/** Band "Công nợ": balances at the end of the period, what it added and settled, the next 7 days and the largest balances. */
export function DebtBand({ data, onPay }: DebtBandProps) {
  const { receivable: ar, payable: ap } = data.debt;
  const asOf = balanceDate(data.period, data.today);

  const topColumns = (direction: DebtDirection): Column<TopParty>[] => [
    {
      key: "name",
      header: direction === "receivable" ? "Khách" : "Nhà cung cấp",
      cell: (r) => <TextLink to={`${LEDGER_PATH[direction]}?party=${encodeURIComponent(r.party_id)}`}>{r.name}</TextLink>,
    },
    { key: "amount", header: "Còn nợ", numeric: true, cell: (r) => <b>{money(r.amount)}</b> },
    // As in the prototype: customers show the overdue amount beside the date, suppliers mark a past date.
    ...(direction === "receivable"
      ? [
          { key: "overdue", header: "Quá hạn", numeric: true, cell: (r: TopParty) => (dec(r.overdue).gt(0) ? badText(vnd(r.overdue)) : "—") },
          { key: "due", header: "Hạn gần nhất", cell: (r: TopParty) => fd(r.next_due) },
        ]
      : [
          {
            key: "due",
            header: "Hạn gần nhất",
            cell: (r: TopParty) => (
              <>
                {fd(r.next_due)}
                {r.next_due < data.today ? (
                  <>
                    {" "}
                    <StatusPill label="Quá hạn" />
                  </>
                ) : null}
              </>
            ),
          },
        ]),
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (r) => (
        <Button size="sm" aria-label={`${payLabel(direction)} – ${r.name}`} onClick={() => onPay(direction, r.party_id)}>
          {payLabel(direction)}
        </Button>
      ),
    },
  ];

  return (
    <>
      <Band title="Công nợ" note={`Số dư tại ${fd(asOf)} · phát sinh và thanh toán trong ${periodLabel(data.period)}`}>
        <Grid cols="g4">
          <KpiCard
            label="Khách còn nợ"
            value={vnd(ar.balance)}
            sub={dec(ar.overdue).gt(0) ? badText(`Quá hạn ${vnd(ar.overdue)}`) : "Không có khoản quá hạn"}
            to={LEDGER_PATH.receivable}
          />
          <KpiCard label="Nợ khách trong kỳ" value={`+${vnd(ar.increase)}`} sub={`Khách đã trả ${vnd(ar.settled)}`} to={LEDGER_PATH.receivable} />
          <KpiCard
            label="Cửa hàng còn nợ NCC"
            value={vnd(ap.balance)}
            sub={dec(ap.overdue).gt(0) ? badText(`Quá hạn ${vnd(ap.overdue)}`) : "Không có khoản quá hạn"}
            to={LEDGER_PATH.payable}
          />
          <KpiCard label="Nợ NCC trong kỳ" value={`+${vnd(ap.increase)}`} sub={`Cửa hàng đã trả ${vnd(ap.settled)}`} to={LEDGER_PATH.payable} />
        </Grid>
        <Grid cols="g2" style={{ marginTop: 20 }}>
          <KpiCard forecast label="Sắp phải thu (7 ngày tới)" value={vnd(ar.due_soon)} sub="Nợ khách đến hạn · dự báo" to={SCHEDULE_PATH} />
          <KpiCard forecast label="Sắp phải trả (7 ngày tới)" value={vnd(ap.due_soon)} sub="Nợ NCC đến hạn · dự báo" to={SCHEDULE_PATH} />
        </Grid>
      </Band>
      <Grid cols="g2" className="band">
        <Card title="Khách nợ nhiều nhất" note={`tại ${fd(asOf)}`}>
          <DataTable caption="Khách nợ nhiều nhất" columns={topColumns("receivable")} rows={ar.top} rowKey={(r) => r.party_id} flat empty="Không có khách còn nợ." />
        </Card>
        <Card title="Nợ nhà cung cấp" note={`tại ${fd(asOf)}`}>
          <DataTable caption="Nợ nhà cung cấp" columns={topColumns("payable")} rows={ap.top} rowKey={(r) => r.party_id} flat empty="Không nợ nhà cung cấp." />
        </Card>
      </Grid>
    </>
  );
}
