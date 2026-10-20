import { dec } from "../../../shared/lib/decimal";
import { fd, money, vnd } from "../../../shared/lib/format";
import { Band } from "../../../shared/ui/band";
import { Card } from "../../../shared/ui/card";
import { CategoryList, type CategoryItem } from "../../../shared/ui/category-list";
import { GroupedBarChart, type GroupedPoint } from "../../../shared/ui/charts/grouped-bar-chart";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DeltaIndicator } from "../../../shared/ui/delta-indicator";
import { DocNo } from "../../../shared/ui/doc-no";
import { Grid } from "../../../shared/ui/grid";
import { KpiCard } from "../../../shared/ui/kpi-card";
import { balanceDate, periodLabel } from "../period-label";
import type { Dashboard, DashboardCategory, DashboardEntry } from "../schemas";

/** Most entries the API lists for a day. */
const ENTRY_LIMIT = 100;

type Flow = "in" | "out";

/** A reversal counts on the side of the entry it reverses, as in the period totals. */
const isInflow = (e: DashboardEntry) => (e.reversal ? dec(e.amount).neg() : dec(e.amount)).gt(0);

/** One side of "Theo loại": the kinds that moved money that way, largest first. */
const categoriesOf = (list: readonly DashboardCategory[], flow: Flow): CategoryItem[] =>
  list
    .filter((c) => dec(c[flow]).gt(0))
    .sort((a, b) => dec(b[flow]).cmp(dec(a[flow])))
    .map((c) => ({ label: c.label, amount: c[flow] }));

/** Year bars read "T10"; day bars keep the API's "12/10". */
const barLabel = (kind: Dashboard["period"]["kind"], label: string, from: string) => (kind === "year" ? `T${Number(from.slice(5, 7))}` : label);

/** Band "Thu – chi": money in and out of the period beside the previous one, the day's entries or the bars, and both by kind. */
export function CashBand({ data }: { data: Dashboard }) {
  const { cash, period } = data;
  const label = periodLabel(period);
  const asOf = balanceDate(period, data.today);
  const net = dec(cash.net);
  const unit = period.kind === "day" ? "none" : period.kind === "year" ? "month" : "day";
  const title = unit === "none" ? "Giao dịch thu – chi trong ngày" : unit === "month" ? "Thu – chi theo tháng" : "Thu – chi theo ngày";

  const columns: Column<DashboardEntry>[] = [
    { key: "doc", header: "Chứng từ", cell: (e) => <DocNo value={e.doc_no} />, footer: "Tổng" },
    { key: "category", header: "Loại", cell: (e) => e.category },
    { key: "party", header: "Đối tượng", cell: (e) => e.party ?? "" },
    { key: "fund", header: "Quỹ", cell: (e) => e.fund },
    { key: "in", header: "Thu", numeric: true, cell: (e) => (isInflow(e) ? money(e.amount) : ""), footer: money(cash.in) },
    { key: "out", header: "Chi", numeric: true, cell: (e) => (isInflow(e) ? "" : money(dec(e.amount).neg())), footer: money(cash.out) },
  ];
  const bars: GroupedPoint<Flow>[] = data.series.map((s) => ({ x: s.from, label: barLabel(period.kind, s.label, s.from), values: { in: s.in, out: s.out } }));

  return (
    <>
      <Band title="Thu – chi" note={label}>
        <Grid cols="g4">
          <KpiCard
            label="Tổng thu"
            value={<span style={{ color: "var(--series-1)" }}>{vnd(cash.in)}</span>}
            sub={<DeltaIndicator current={cash.in} previous={cash.prev_in} />}
            to={cash.link}
          />
          <KpiCard
            label="Tổng chi"
            value={<span style={{ color: "var(--series-2)" }}>{vnd(cash.out)}</span>}
            sub={<DeltaIndicator current={cash.out} previous={cash.prev_out} goodWhenUp={false} />}
            to={cash.link}
          />
          <KpiCard
            label="Chênh lệch thu – chi"
            value={vnd(cash.net)}
            sub={net.gte(0) ? "Dòng tiền dương trong kỳ" : <b style={{ color: "var(--bad-ink)" }}>Chi nhiều hơn thu</b>}
            to={cash.link}
          />
          <KpiCard label="Tiền trong quỹ" value={vnd(cash.balance)} sub={`Tại ngày ${fd(asOf)}`} to={cash.link} />
        </Grid>
      </Band>
      <Grid cols="g-main" className="band">
        <Card title={title} note={label}>
          {unit === "none" ? (
            <>
              <DataTable caption={title} columns={columns} rows={data.entries} rowKey={(e) => e.id} flat empty="Không có giao dịch thu – chi trong ngày này." />
              {data.entries.length >= ENTRY_LIMIT ? <p className="hint">Đang hiện {ENTRY_LIMIT} giao dịch gần nhất; dòng Tổng tính cả ngày.</p> : null}
            </>
          ) : (
            <GroupedBarChart
              caption={title}
              data={bars}
              xHeader={unit === "month" ? "Tháng" : "Ngày"}
              series={[
                { key: "in", name: "Thu", color: "var(--series-1)" },
                { key: "out", name: "Chi", color: "var(--series-2)" },
              ]}
            />
          )}
        </Card>
        <Card title="Theo loại" note={label}>
          <h3 className="mini-h" style={{ color: "var(--series-1)" }}>
            Thu {vnd(cash.in)}
          </h3>
          <CategoryList items={categoriesOf(cash.by_category, "in")} total={cash.in} />
          <h3 className="mini-h" style={{ color: "var(--accent-orange-strong)", marginTop: 16 }}>
            Chi {vnd(cash.out)}
          </h3>
          <CategoryList items={categoriesOf(cash.by_category, "out")} total={cash.out} />
        </Card>
      </Grid>
    </>
  );
}
