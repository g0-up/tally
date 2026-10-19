import { errorMessage } from "../../../shared/api/api-error";
import { useServerBusinessDate } from "../../../shared/api/platform-queries";
import { addDays, todayBusinessDate } from "../../../shared/lib/dates";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { fd, money, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Grid } from "../../../shared/ui/grid";
import { KpiCard } from "../../../shared/ui/kpi-card";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { useDebtForecast, useFunds } from "../api/queries";
import type { DebtForecast } from "../schemas";

/** Days ahead the KPIs cover, and the days the table lists. */
const KPI_DAYS = 7;
const TABLE_DAYS = 30;

type Day = DebtForecast["days"][number];
type Row = { key: string; date: string | null; receivable: string; payable: string; promised: string; confirmedIn: string; confirmedOut: string };

const blank = (v: string) => (dec(v).gt(0) ? money(v) : "");

/**
 * Lịch thu/trả (/debts/schedule): what falls due per day on both ledgers and what customers
 * promised, next to the money actually confirmed. A forecast, not cash in hand.
 */
export default function SchedulePage() {
  const serverToday = useServerBusinessDate();
  const today = serverToday ?? todayBusinessDate();
  const forecast = useDebtForecast(today, addDays(today, TABLE_DAYS), serverToday !== null);
  const funds = useFunds();
  const f = forecast.data;

  const within = (d: Day) => d.date <= addDays(today, KPI_DAYS);
  const inSoon = f ? dec(f.overdue_receivable).plus(sumDec(f.days.filter(within).map((d) => d.receivable_due))) : null;
  const outSoon = f ? dec(f.overdue_payable).plus(sumDec(f.days.filter(within).map((d) => d.payable_due))) : null;
  const cash = funds.data ? sumDec(funds.data.map((x) => x.balance)) : null;

  const rows: Row[] = f
    ? [
        ...(dec(f.overdue_receivable).gt(0) || dec(f.overdue_payable).gt(0)
          ? [{ key: "overdue", date: null, receivable: f.overdue_receivable, payable: f.overdue_payable, promised: "0", confirmedIn: "0", confirmedOut: "0" }]
          : []),
        ...f.days
          .filter((d) => [d.receivable_due, d.payable_due, d.promised, d.confirmed_in, d.confirmed_out].some((v) => dec(v).gt(0)))
          .map((d) => ({ key: d.date, date: d.date, receivable: d.receivable_due, payable: d.payable_due, promised: d.promised, confirmedIn: d.confirmed_in, confirmedOut: d.confirmed_out })),
      ]
    : [];
  const columns: Column<Row>[] = [
    { key: "date", header: "Hạn", cell: (r) => (r.date === null ? <StatusPill label="Quá hạn" tone="bad" /> : fd(r.date)) },
    { key: "in", header: "Phải thu đến hạn", numeric: true, cell: (r) => blank(r.receivable) },
    { key: "out", header: "Phải trả đến hạn", numeric: true, cell: (r) => blank(r.payable) },
    { key: "promised", header: "Cam kết", numeric: true, cell: (r) => blank(r.promised) },
    { key: "cin", header: "Đã thu", numeric: true, cell: (r) => blank(r.confirmedIn) },
    { key: "cout", header: "Đã chi", numeric: true, cell: (r) => blank(r.confirmedOut) },
  ];
  const hasPromise = f?.days.some((d) => dec(d.promised).gt(0)) ?? false;

  return (
    <>
      <PageHeader lead="Dự kiến theo hạn và cam kết của khách. Đây là dự báo, khác với tiền chắc chắn đã nhận." />
      {forecast.isError ? (
        <Callout tone="bad" role="alert">
          {errorMessage(forecast.error)}
        </Callout>
      ) : null}
      <Grid cols="g3">
        <KpiCard forecast label="Dự kiến thu 7 ngày" value={inSoon ? vnd(inSoon) : "—"} sub="Gồm cả khoản quá hạn" />
        <KpiCard forecast label="Dự kiến trả 7 ngày" value={outSoon ? vnd(outSoon) : "—"} sub="Nợ NCC đến hạn" />
        <KpiCard label="Tiền đang có" value={cash ? vnd(cash) : "—"} sub="Số thực trong quỹ" />
      </Grid>
      {forecast.isPending ? <ListLoading /> : <DataTable caption={`Lịch thu/trả ${TABLE_DAYS} ngày tới`} columns={columns} rows={rows} rowKey={(r) => r.key} />}
      {f && !hasPromise ? <p className="hint">Ưu tiên nhắc nợ: khoản quá hạn lâu nhất trước. Ghi chú hẹn trả ở hồ sơ khách để cập nhật cột “Cam kết”.</p> : null}
    </>
  );
}
