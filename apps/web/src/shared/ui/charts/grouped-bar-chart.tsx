import { Fragment } from "react";
import { dec } from "../../lib/decimal";
import { axisLabel, fd, money, vnd } from "../../lib/format";
import { DataTable } from "../data-table/data-table";
import type { Column } from "../data-table/columns";
import { ChartFrame, type ChartHit } from "./chart-frame";
import { amount, roundedTopPath, yScale, type ChartPoint, type ChartSeries } from "./chart-math";

const W = 680;
const H = 240;
const L = 62;
const B = 26;
const T = 12;
const R = 8;
const GAP = 2;

export interface GroupedPoint<K extends string> extends ChartPoint<K> {
  /** Axis label: "12/10" for days, "T10" for months. */
  label: string;
}

export interface GroupedBarChartProps<K extends string> {
  data: ReadonlyArray<GroupedPoint<K>>;
  /** Two series; the tooltip and table show first − second as "Chênh lệch". */
  series: readonly [ChartSeries<K>, ChartSeries<K>];
  ariaLabel?: string;
  caption: string;
  /** "Ngày" or "Tháng". */
  xHeader?: string;
}

/** Month bars ("T10") read "T10/2026"; day bars read dd/mm/yyyy. */
function pointTitle(p: GroupedPoint<string>): string {
  return p.label.length <= 3 ? `${p.label}/${p.x.slice(0, 4)}` : fd(p.x);
}

/**
 * Prototype `groupedBars()`: side-by-side bars per period (thu / chi), zero tick "0", at most ~10
 * axis labels, and the difference in the tooltip.
 */
export function GroupedBarChart<K extends string>({ data, series, ariaLabel = "Biểu đồ cột thu và chi", caption, xHeader = "Ngày" }: GroupedBarChartProps<K>) {
  const [a, b] = series;
  const max = Math.max(1, ...data.map((r) => Math.max(...series.map((s) => amount(r.values[s.key])))));
  const sc = yScale(max, H, T, B);
  const bw = (W - L - R) / Math.max(1, data.length);
  const w = Math.max(2, Math.min(16, (bw * 0.7 - GAP) / series.length));
  const every = data.length > 16 ? Math.ceil(data.length / 10) : 1;
  const diff = (r: GroupedPoint<K>) => dec(r.values[a.key]).minus(dec(r.values[b.key]));

  const hits: ChartHit[] = data.map((r, i) => ({
    key: r.x,
    x: L + bw * i,
    y: T,
    width: bw,
    height: H - T - B,
    tipLabel: `${pointTitle(r)}: ${series.map((s) => `${s.name} ${vnd(r.values[s.key])}`).join(", ")}, chênh lệch ${vnd(diff(r))}`,
    tip: (
      <>
        <b>{pointTitle(r)}</b>
        {series.map((s) => (
          <Fragment key={s.key}>
            <br />
            {s.name}: {vnd(r.values[s.key])}
          </Fragment>
        ))}
        <br />
        Chênh lệch: {vnd(diff(r))}
      </>
    ),
  }));

  const columns: Column<GroupedPoint<K>>[] = [
    { key: "x", header: xHeader, cell: (r) => pointTitle(r) },
    ...series.map((s) => ({ key: s.key, header: s.tableHeader ?? s.name, numeric: true, cell: (r: GroupedPoint<K>) => money(r.values[s.key]) })),
    { key: "diff", header: "Chênh lệch", numeric: true, cell: (r) => money(diff(r)) },
  ];
  const tableRows = data.filter((r) => series.some((s) => !dec(r.values[s.key]).isZero()));

  return (
    <ChartFrame
      width={W}
      height={H}
      ariaLabel={ariaLabel}
      hits={hits}
      legend={series.map((s) => ({ name: s.name, color: s.color }))}
      table={<DataTable caption={caption} columns={columns} rows={tableRows} rowKey={(r) => r.x} flat empty="Không có giao dịch." />}
    >
      {sc.ticks.map((v) => (
        <Fragment key={v}>
          <line className="gl" x1={L} x2={W - R} y1={sc.y(v)} y2={sc.y(v)} />
          <text className="ax" x={L - 8} y={sc.y(v) + 4} textAnchor="end">
            {axisLabel(v, true)}
          </text>
        </Fragment>
      ))}
      {data.map((r, i) => {
        const cx = L + bw * i + bw / 2;
        const x0 = cx - (w * series.length + GAP * (series.length - 1)) / 2;
        return (
          <g key={r.x}>
            {series.map((s, j) => {
              const v = amount(r.values[s.key]);
              if (!v) return null;
              const yy = sc.y(v);
              const h = Math.max(1, sc.y(0) - yy);
              return <path key={s.key} fill={s.color} d={roundedTopPath(x0 + j * (w + GAP), yy, w, h, Math.min(3, h, w / 2))} />;
            })}
            {i % every === 0 ? (
              <text className="ax" x={cx} y={H - 8} textAnchor="middle">
                {r.label}
              </text>
            ) : null}
          </g>
        );
      })}
      <line x1={L} x2={W - R} y1={sc.y(0)} y2={sc.y(0)} stroke="var(--gray-300)" />
    </ChartFrame>
  );
}
